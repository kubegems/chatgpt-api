package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type ProxyEndpointHandler struct {
	lock sync.Locker
	reg  *ServiceRegistry
}

func NewProxyEndpointHandler(r *ServiceRegistry) *ProxyEndpointHandler {
	return &ProxyEndpointHandler{
		lock: &sync.Mutex{},
		reg:  r,
	}
}

func (h *ProxyEndpointHandler) getInstance(u *url.URL) string {
	// 如果客户端指定了后端，且指定的后端在线，那就使用它
	instance := u.Query().Get("instance")
	if instance != "" && h.reg.isInstanceOnline(instance) {
		return instance
	}
	// 客户端没指定或者指定的后端不在线, 就根据conversationId找
	conversationId := u.Query().Get("conversationId")
	if conversationId != "" {
		instance, online := h.reg.findConversationInstance(conversationId)
		// 如果找到后端节点，且在线，那就用它
		// 如果这个节点不在线，那就从这个节点将会话删除了，重新找一个新节点, 重新生成新会话
		if online {
			return instance
		} else {
			h.reg.removeConversation(conversationId)
			return h.reg.oneEndpoint()
		}
	}
	// 如果没有提供conversationId, 那就选择会话最少的节点
	return h.reg.oneEndpoint()
}

func (h *ProxyEndpointHandler) Proxy(c *gin.Context) {
	u, _ := url.Parse(c.Request.URL.String())
	p := httputil.NewSingleHostReverseProxy(u)
	p.Director = func(req *http.Request) {
		req.URL.Scheme = os.Getenv("CHATAPI_SCHEME")
		req.URL.Host = fmt.Sprintf("%s.%s:%s", h.getInstance(u), os.Getenv("CHATAPI_SVC"), os.Getenv("CHATAPI_SVC_PORT"))
	}
	p.ModifyResponse = func(resp *http.Response) error {
		instance := resp.Header.Get("instance")
		conversationId := resp.Header.Get("conversationId")
		// 上游如果正常，就会返回instance 和 conversationId
		if instance != "" && conversationId != "" && conversationId != "undefined" {
			h.reg.setEndpoint(conversationId, instance)
		}
		return nil
	}
	p.ServeHTTP(c.Writer, c.Request)
}

func (h *ProxyEndpointHandler) Pool(c *gin.Context) {
	c.JSON(200, h.reg.endpoints)
}

func (h *ProxyEndpointHandler) DeleteConversation(c *gin.Context) {
	conversationId := c.Query("conversationId")
	if conversationId != "" {
		h.reg.removeConversation(conversationId)
	}
	c.JSON(200, h.reg.endpoints)
}

func main() {
	exitSignal := make(chan os.Signal)
	signal.Notify(exitSignal, syscall.SIGTERM, syscall.SIGINT)
	ctx, cancel := context.WithCancel(context.Background())
	r := gin.Default()
	reg := NewServiceRegistry(ctx)
	reg.loadCache()
	go func() {
		sig := <-exitSignal
		fmt.Printf("caught sig: %+v", sig)
		reg.storeCache()
		cancel()
		os.Exit(0)
	}()
	h := NewProxyEndpointHandler(reg)
	r.Any("/", h.Proxy)
	r.GET("/pool", h.Pool)
	r.DELETE("/pool", h.DeleteConversation)
	r.Run(":9000")
}

/*
数据结构
{
	ep-chat-1: {
		name: ep-caht-1,
		conversations: [{
			id: "xxx-xxx-xxx",
			begintime: "2020202020"
			latesttime: "2020202020"
		}, {
			id: "xxx-xxx-xxx",
			begintime: "2020202020"
			latesttime: "2020202020"
		}],
		online: true
	}
}
*/

type Conversation struct {
	Id         string
	BeginTime  *time.Time
	LatestTime *time.Time
}

type Endpoint struct {
	Name          string
	Conversations map[string]*Conversation
	Online        bool
	lock          sync.Locker
}

func (ep *Endpoint) Weight() int {
	return len(ep.Conversations)
}

func (ep *Endpoint) conversationOnNode(conversationId string) bool {
	_, exist := ep.Conversations[conversationId]
	return exist
}

func (ep *Endpoint) removeConversation(conversationId string) {
	ep.lock.Lock()
	defer ep.lock.Unlock()
	delete(ep.Conversations, conversationId)
}

type ServiceRegistry struct {
	cli       *kubernetes.Clientset
	endpoints map[string]*Endpoint
	lock      sync.Locker
}

func NewServiceRegistry(ctx context.Context) *ServiceRegistry {
	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}
	// creates the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
	r := &ServiceRegistry{
		cli:       clientset,
		endpoints: map[string]*Endpoint{},
		lock:      &sync.Mutex{},
	}
	go r.watch(ctx)
	return r
}

func (r *ServiceRegistry) removeConversation(conversationId string) {
	for _, ep := range r.endpoints {
		if ep.conversationOnNode(conversationId) {
			ep.removeConversation(conversationId)
		}
	}
}

func (r *ServiceRegistry) storeCache() {
	bts, _ := json.Marshal(r.endpoints)
	os.WriteFile("/cache/.registry_cache", bts, os.FileMode(0644))
}

func (r *ServiceRegistry) loadCache() {
	cacheFile := "/cache/.registry_cache"
	_, err := os.Stat(cacheFile)
	if os.IsNotExist(err) {
		return
	}
	bts, err := os.ReadFile(cacheFile)
	if err != nil {
		return
	}
	json.Unmarshal(bts, &r.endpoints)
	for _, ep := range r.endpoints {
		if ep.lock == nil {
			ep.lock = &sync.Mutex{}
		}
	}
}

func (r *ServiceRegistry) setEndpoint(conversationID, instance string) {
	ep, exist := r.endpoints[instance]
	if exist {
		now := time.Now()
		if conversation, exist := ep.Conversations[conversationID]; exist {
			conversation.LatestTime = &now
		} else {
			ep.lock.Lock()
			defer ep.lock.Unlock()
			ep.Conversations[conversationID] = &Conversation{
				Id:         conversationID,
				BeginTime:  &now,
				LatestTime: &now,
			}
		}
	}
}

func (r *ServiceRegistry) findConversationInstance(conversationId string) (string, bool) {
	for _, ep := range r.endpoints {
		if ep.conversationOnNode(conversationId) {
			return ep.Name, ep.Online
		}
	}
	return "", false
}

func (r *ServiceRegistry) isInstanceOnline(instanceName string) bool {
	for _, ep := range r.endpoints {
		if ep.Name == instanceName {
			return ep.Online
		}
	}
	return false
}

// 找到最小权重的在线节点
func (r *ServiceRegistry) oneEndpoint() string {
	var (
		minWeightEp string
	)
	tmpWeight := 10000
	for _, ep := range r.endpoints {
		epWeight := ep.Weight()
		if ep.Online && epWeight < tmpWeight {
			tmpWeight = epWeight
			minWeightEp = ep.Name
		}
	}
	return minWeightEp
}

func (r *ServiceRegistry) watch(ctx context.Context) {
	ns := os.Getenv("NAMESPACE")
	watch, err := r.cli.CoreV1().Endpoints(ns).Watch(ctx, metav1.ListOptions{
		Watch: true,
	})
	if err != nil {
		panic(err)
	}
	for {
		evt := <-watch.ResultChan()
		ep, ok := evt.Object.(*corev1.Endpoints)
		if !ok {
			continue
		}
		if ep.Name != os.Getenv("CHATAPI_SVC") {
			continue
		}
		currentEndpoints := map[string]bool{}
		for _, sub := range ep.Subsets {
			for _, addr := range sub.Addresses {
				currentEndpoints[addr.Hostname] = true
			}
		}
		r.lock.Lock()
		for currentEp := range currentEndpoints {
			if _, exist := r.endpoints[currentEp]; !exist {
				r.endpoints[currentEp] = &Endpoint{
					Name:          currentEp,
					Conversations: map[string]*Conversation{},
					Online:        true,
					lock:          &sync.Mutex{},
				}
			} else {
				r.endpoints[currentEp].Online = true
			}
		}
		for ep := range r.endpoints {
			if _, exist := currentEndpoints[ep]; !exist {
				r.endpoints[ep].Online = false
			}
		}
		r.lock.Unlock()
	}
}
