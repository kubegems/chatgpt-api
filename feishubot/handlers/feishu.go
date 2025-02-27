package handlers

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/goccy/go-json"
	"github.com/google/uuid"
	sdkginext "github.com/larksuite/oapi-sdk-gin"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	log "github.com/sirupsen/logrus"
)

const (
	ChatGPTHost             = "ChatGPTHost"
	FeishuBotName           = "FeishuBotName"
	FeishuAppID             = "FeishuAppID"
	FeishuAppSecret         = "FeishuAppSecret"
	FeishuVerificationToken = "FeishuVerificationToken"
	FeishuEventEncryptKey   = "FeishuEventEncryptKey"
	FilterPlugins           = "FilterPlugins"
	ProhibitedWords         = "ProhibitedWords"
	FilterReplaceText       = "FilterReplaceText"
	ShowSource              = "ShowSource"
	CustomErrorMessage      = "CustomErrorMessage"
	RetryTime               = "RetryTime"
)

type APIIface interface {
	GetConversation(inputs, conversation_id, parent_id string) (reply, conversationId, parentId, source string, err error)
}

type MessageFilter interface {
	Filter(text string) string
}

type FeishuHandler struct {
	ctx           context.Context
	dispatcher    *dispatcher.EventDispatcher
	sessions      map[string]*FeishuSession
	sessionLock   sync.RWMutex
	cli           *lark.Client
	messageFilter []MessageFilter

	api APIIface
	opt *FeishuOptions
}

type FeishuOptions struct {
	ChatGPTHost               string
	FeishuAppID               string
	FeishuAppSecret           string
	FeishuBotName             string
	FeishuVerificationToken   string
	FeishuEventEncryptKey     string
	ConversationExpireSeconds int64
	FilterReplaceText         string
	ShowSource                bool
	Filters                   []string
	RetryTime                 int
	CustomErrorMessage        string
}

func getEnvDefault(key, defaultv string) string {
	v := os.Getenv(key)
	if v != "" {
		return v
	}
	return defaultv
}

func (h *FeishuHandler) Filter(text string) string {
	out := text
	for _, filter := range h.messageFilter {
		out = filter.Filter(out)
	}
	return out
}

func getEnvList(key string) []string {
	return strings.Split(os.Getenv(key), ",")
}

func getEnvBool(key string) bool {
	return os.Getenv("ShowSource") == "yes"
}

func getEnvInt(key string) int {
	retryTime, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return 0
	}
	return retryTime
}

func NewFeishuOptions() *FeishuOptions {
	var expireSeconds int64
	secondStr := getEnvDefault("ConversationExpireSeconds", "")
	if secondStr != "" {
		seconds, err := strconv.ParseInt(secondStr, 10, 64)
		if err != nil {
			panic("ConversationExpireSeconds must be a integer")
		}
		expireSeconds = seconds
	}
	opt := &FeishuOptions{
		ChatGPTHost:               getEnvDefault(ChatGPTHost, "chatgpt-api"),
		FeishuBotName:             getEnvDefault(FeishuBotName, "chatgpt-bot"),
		FeishuAppID:               getEnvDefault(FeishuAppID, ""),
		FeishuAppSecret:           getEnvDefault(FeishuAppSecret, ""),
		FeishuVerificationToken:   getEnvDefault(FeishuVerificationToken, ""),
		FeishuEventEncryptKey:     getEnvDefault(FeishuEventEncryptKey, ""),
		Filters:                   getEnvList(FilterPlugins),
		FilterReplaceText:         getEnvDefault(FilterReplaceText, "..."),
		ShowSource:                getEnvBool(ShowSource),
		CustomErrorMessage:        getEnvDefault(CustomErrorMessage, "Sorry, I can't deal with you question"),
		RetryTime:                 getEnvInt(RetryTime),
		ConversationExpireSeconds: expireSeconds,
	}
	if opt.FeishuAppID == "" || opt.FeishuAppSecret == "" || opt.FeishuVerificationToken == "" {
		panic("Environment variable (FeishuAppID, FeishuAppSecret, FeishuVerificationToken) must provide")
	}
	return opt
}

func NewFeishuHandler(opt *FeishuOptions) *FeishuHandler {
	cli := lark.NewClient(opt.FeishuAppID, opt.FeishuAppSecret)
	h := &FeishuHandler{
		ctx:         context.Background(),
		sessions:    map[string]*FeishuSession{},
		sessionLock: sync.RWMutex{},
		cli:         cli,
		opt:         opt,
	}
	filters := []MessageFilter{}
	for _, filterName := range opt.Filters {
		f, exist := predefinedFilters[filterName]
		if exist {
			filters = append(filters, f)
		}
	}
	h.messageFilter = filters
	h.api = NewChatGPTAPI(opt.ChatGPTHost)
	h.dispatcher = dispatcher.NewEventDispatcher(
		opt.FeishuVerificationToken,
		opt.FeishuEventEncryptKey,
	).OnP2MessageReceiveV1(
		h.OnP2ReceiveMessage,
	)
	return h
}

func (h *FeishuHandler) OnP2ReceiveMessage(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	// 目前只处理单聊，和群聊
	if !(*event.Event.Message.ChatType == "p2p" || *event.Event.Message.ChatType == "group") {
		log.WithField("ChatType", event.Event.Message.ChatType).Debug("ignore")
		return nil
	}
	// 如果是群聊，只处理 @ 了机器人的情况，且只@机器人一个
	if *event.Event.Message.ChatType == "group" {
		menthioned := false
		if len(event.Event.Message.Mentions) != 1 {
			return nil
		}
		for _, mention := range event.Event.Message.Mentions {
			if *mention.Name == h.opt.FeishuBotName {
				menthioned = true
			}
		}
		if !menthioned {
			return nil
		}
	}
	h.handleChatEvent(event)
	return nil
}

func (h *FeishuHandler) StartEventLoop() {
	go h.sessionLoop()
}

func (h *FeishuHandler) handleChatEvent(event *larkim.P2MessageReceiveV1) {
	session_id := fmt.Sprintf("%s_%s", *event.Event.Message.ChatId, *event.Event.Sender.SenderId.UnionId)
	session := h.addOrRefreshSession(session_id, *event.Event.Message.ChatId, *event.Event.Sender.SenderId.UnionId)
	session.msgch <- event.Event
}

func (h *FeishuHandler) addOrRefreshSession(session_id, chat_id, sender_id string) *FeishuSession {
	_, exist := h.sessions[session_id]
	if exist {
		h.sessions[session_id].RefreshExpire()
	} else {
		ctx, cancel := context.WithCancel(context.Background())
		h.sessionLock.Lock()
		defer h.sessionLock.Unlock()
		h.sessions[session_id] = NewFeishuSession(ctx, cancel, session_id, chat_id, sender_id, h)
		go h.sessions[session_id].Transfer(h.cli)
	}
	return h.sessions[session_id]
}

func (h *FeishuHandler) sessionLoop() {
	for {
		time.Sleep(time.Second * 5)
		todel := []string{}
		for session_id, session := range h.sessions {
			if session.IsExpired() {
				todel = append(todel, session_id)
			}
		}
		for _, session_id := range todel {
			h.sessionLock.Lock()
			log.Infoln("session expired: ", session_id)
			h.sessions[session_id].cancel()
			delete(h.sessions, session_id)
			h.sessionLock.Unlock()
		}
	}
}

func (h *FeishuHandler) EventHandler() func(c *gin.Context) {
	return sdkginext.NewEventHandlerFunc(h.dispatcher)
}

type FeishuSession struct {
	ctx      context.Context
	cancel   context.CancelFunc
	id       string
	expireAt int64
	chatid   string
	senderid string
	msgch    chan *larkim.P2MessageReceiveV1Data
	api      APIIface
	h        *FeishuHandler
	lastTime *time.Time

	conversation_id string
	parent_id       string
}

func (s *FeishuSession) IsExpired() bool {
	now := time.Now().Unix()
	if now > s.expireAt {
		log.Debugln(now, s.expireAt)
		return true
	}
	return false
}

func (s *FeishuSession) RefreshExpire() {
	s.expireAt = time.Now().Unix() + s.h.opt.ConversationExpireSeconds
}

func (s *FeishuSession) Transfer(cli *lark.Client) {
	for {
		select {
		case msg := <-s.msgch:
			textContent, err := getTextContent(msg)
			if err != nil {
				log.Errorln("can't parse content ", *msg.Message.Content)
			} else {
				if s.lastTime != nil {
					nextTime := s.lastTime.Add(time.Second * 6)
					if time.Now().Before(nextTime) {
						d := time.Until(nextTime)
						<-time.After(d)
					}

				}

				replyText, newConversationId, newParentId, source, err := s.api.GetConversation(textContent, s.conversation_id, s.parent_id)
				if err != nil {
					log.Errorf("upstream api invoke failed, %v\n", err)
					replyText = s.h.opt.CustomErrorMessage
				}
				if newConversationId != "" {
					s.conversation_id = newConversationId
				}
				if newParentId != "" {
					s.parent_id = newParentId
				}
				s.RefreshExpire()
				atUser := *msg.Message.ChatType == "group"
				if s.h.messageFilter != nil {
					replyText = s.h.Filter(replyText)
				}
				resp, err := cli.Im.Message.Reply(context.Background(), replyMessage(msg, replyText, source, atUser, s.h.opt.ShowSource))
				if err != nil {
					log.Errorf("send message failed, %v\n", err)
				} else {
					log.Info("send message succeed ", resp.Success(), resp.Err, resp.Error())
				}
			}

		case <-s.ctx.Done():
			resp, err := cli.Im.Message.Create(context.Background(), byebye(s.chatid, s.senderid))
			if err != nil {
				log.Errorf("send message failed, %v\n", err)
			} else {
				log.Info("send message succeed ", resp.Success(), resp.Err, resp.Error())
			}
			return
		}
	}
}

func byebye(chatid, userid string) *larkim.CreateMessageReq {
	return larkim.NewCreateMessageReqBuilder().ReceiveIdType("chat_id").Body(
		larkim.NewCreateMessageReqBodyBuilder().ReceiveId(
			chatid,
		).MsgType(
			larkim.MsgTypeText,
		).Uuid(
			uuid.New().String(),
		).Content(
			larkim.NewTextMsgBuilder().AtUser(userid, "").TextLine("会话结束了，拜拜!").Build(),
		).Build(),
	).Build()
}

func replyMessage(message *larkim.P2MessageReceiveV1Data, replyContent, source string, atUser bool, showSource bool) *larkim.ReplyMessageReq {
	var (
		text, sourceText string
	)
	if showSource {
		sourceText = fmt.Sprintf("【本次对话由 %s 提供】", source)
	}
	if atUser {
		text = fmt.Sprintf("<at user_id=\"%s\">Tom </at> %s \n %s", *message.Sender.SenderId.UnionId, replyContent, sourceText)
	} else {
		text = fmt.Sprintf("%s \n%s", replyContent, sourceText)
	}
	tmp := struct {
		Text string `json:"text"`
	}{
		Text: text,
	}
	textContent, err := json.Marshal(tmp)
	if err != nil {
		log.Error(err)
	}
	/*
		TODO: sdk在序列化json数据的时候，似乎有点bug，回头来看
		textContent := larkim.NewTextMsgBuilder().
			AtUser(*message.Sender.SenderId.UserId, "tom").
			TextLine(replyContent).
			Text(sourceLine).
			Build()
	*/
	uid := uuid.New().String()
	return larkim.NewReplyMessageReqBuilder().MessageId(
		*message.Message.MessageId,
	).Body(
		larkim.NewReplyMessageReqBodyBuilder().MsgType(
			"text",
		).Uuid(
			uid,
		).Content(
			string(textContent),
		).Build(),
	).Build()
}

func NewFeishuSession(ctx context.Context, cancel context.CancelFunc, id, chat_id, sender_id string, h *FeishuHandler) *FeishuSession {
	s := &FeishuSession{
		ctx:      ctx,
		h:        h,
		cancel:   cancel,
		id:       id,
		chatid:   chat_id,
		senderid: sender_id,
		msgch:    make(chan *larkim.P2MessageReceiveV1Data, 100),
		api:      h.api,
	}
	s.RefreshExpire()
	return s
}

type Text struct {
	Text string `json:"text,omitempty"`
}

func (t *Text) GetText() string {
	s := strings.ReplaceAll(t.Text, "@_user_1", "")
	return strings.TrimSpace(s)
}

func (t *Text) CmdKind() string {
	return ""
}

func getTextContent(event *larkim.P2MessageReceiveV1Data) (string, error) {
	text := &Text{}
	err := json.Unmarshal([]byte(*event.Message.Content), text)
	if err != nil {
		return "", err
	}
	return text.GetText(), nil
}
