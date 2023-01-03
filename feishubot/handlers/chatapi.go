package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

type ChatGPTAPI struct {
	host string
}

func NewChatGPTAPI(host string) *ChatGPTAPI {
	return &ChatGPTAPI{host: host}
}

func (api *ChatGPTAPI) buildDirectRequest(inputs, cid, pid string) (*http.Request, error) {
	u, err := url.Parse(api.host)
	if err != nil {
		return nil, err
	}
	query := u.Query()
	query.Add("q", inputs)
	if cid != "" {
		query.Add("conversationId", cid)
	}
	if pid != "" {
		query.Add("parentMessageId", pid)
	}
	query.Add("messageId", uuid.New().String())
	u.RawQuery = query.Encode()
	return http.NewRequest(http.MethodGet, u.String(), nil)
}

func (api *ChatGPTAPI) doRequest(req *http.Request, resp Resp) error {
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		log.WithField("STAGE", "api call error").Error(err)
		return err
	}
	if response.StatusCode >= 300 {
		bts, _ := io.ReadAll(response.Body)
		log.WithField("STAGE", "api call failed").WithField("HTTP_STATUS_CODE", response.StatusCode).Error(string(bts))
		err = fmt.Errorf("invalid HTTP StatusCode %d", response.StatusCode)
		return err
	}
	if err = json.NewDecoder(response.Body).Decode(resp); err != nil {
		log.
			WithField("STAGE", "decode api response failed").
			WithField("url", req.URL.String()).Error(err)
		return err
	}
	return nil
}

func (api *ChatGPTAPI) getRequest(inputs, cid, pid string) (*http.Request, error) {
	req, err := api.buildDirectRequest(inputs, cid, pid)
	if err != nil {
		log.WithField("STAGE", "build request failed").Error(err)
	}
	return req, err
}

func (api *ChatGPTAPI) GetConversation(inputs, cid, pid string) (reply, conversationId, parentId, source string, err error) {
	var (
		resp Resp
		req  *http.Request
	)
	req, err = api.getRequest(inputs, cid, pid)
	if err != nil {
		return
	}
	resp = &RawBody{}
	if err = api.doRequest(req, resp); err != nil {
		return
	}
	retryTime := 5
	for !resp.IsOK() && retryTime > 0 {
		// chatgpt-api 可能出现刷新session，等待三秒重试
		log.WithField("STAGE", "retry api call").Debug(inputs)
		<-time.After(time.Second * 3)
		req, _ = api.getRequest(inputs, cid, pid)
		if err = api.doRequest(req, resp); err != nil {
			log.WithField("STAGE", "retry api call failed").WithFields(resp.Fields()).Error(err)
			return
		}
		retryTime--
	}
	reply = resp.GetReply()
	conversationId = resp.GetConversationID()
	parentId = resp.GetMessageID()
	source = resp.GetSource()
	log.WithField("Input", inputs).WithFields(resp.Fields()).Debug(reply)
	return
}

type RawBody struct {
	Reply          string `json:"response"`
	ConversationID string `json:"conversationId"`
	MessageID      string `json:"messageId"`
	Source         string `json:"source"`
	Instance       string `json:"instance"`
}

type Resp interface {
	IsOK() bool
	GetReply() string
	GetConversationID() string
	GetMessageID() string
	GetSource() string
	GetInstance() string
	Fields() map[string]interface{}
}

func (r *RawBody) IsOK() bool {
	isOK := r.ConversationID != ""
	return isOK
}

func (r *RawBody) GetConversationID() string {
	return r.ConversationID
}

func (r *RawBody) GetMessageID() string {
	return r.MessageID
}

func (r *RawBody) GetSource() string {
	if r.Instance != "" {
		return fmt.Sprintf("%s(%s)", r.Source, r.Instance)
	}
	return r.Source
}

func (r *RawBody) GetInstance() string {
	return r.Instance
}

func (r *RawBody) GetReply() string {
	return r.Reply
}

func (r *RawBody) Fields() map[string]interface{} {
	return map[string]interface{}{
		"conversationId":    r.ConversationID,
		"messageId":         r.MessageID,
		"instance":          r.Instance,
		"ValidResponseBody": r.IsOK(),
	}
}
