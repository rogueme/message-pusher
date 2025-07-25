package channel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"message-pusher/common"
	"message-pusher/model"
	"net/http"
	"strings"
	"time"
)

type wechatCorpAccountResponse struct {
	ErrorCode    int    `json:"errcode"`
	ErrorMessage string `json:"errmsg"`
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type WeChatCorpAccountTokenStoreItem struct {
	CorpId      string
	AgentSecret string
	AgentId     string
	AccessToken string
}

func (i *WeChatCorpAccountTokenStoreItem) Key() string {
	return i.CorpId + i.AgentId + i.AgentSecret
}

func (i *WeChatCorpAccountTokenStoreItem) IsShared() bool {
	appId := fmt.Sprintf("%s|%s", i.CorpId, i.AgentId)
	var count int64 = 0
	model.DB.Model(&model.Channel{}).Where("secret = ? and app_id = ? and type = ?",
		i.AgentSecret, appId, model.TypeWeChatCorpAccount).Count(&count)
	return count > 1
}

func (i *WeChatCorpAccountTokenStoreItem) IsFilled() bool {
	return i.CorpId != "" && i.AgentSecret != "" && i.AgentId != ""
}

func (i *WeChatCorpAccountTokenStoreItem) Token() string {
	return i.AccessToken
}

func (i *WeChatCorpAccountTokenStoreItem) Refresh() {
	// https://work.weixin.qq.com/api/doc/90000/90135/91039
	client := http.Client{
		Timeout: 5 * time.Second,
	}
	req, err := http.NewRequest("GET", fmt.Sprintf("https://qyapi.weixin.qq.com/cgi-bin/gettoken?corpid=%s&corpsecret=%s",
		i.CorpId, i.AgentSecret), nil)
	if err != nil {
		common.SysError(err.Error())
		return
	}
	responseData, err := client.Do(req)
	if err != nil {
		common.SysError("failed to refresh access token: " + err.Error())
		return
	}
	defer responseData.Body.Close()
	var res wechatCorpAccountResponse
	err = json.NewDecoder(responseData.Body).Decode(&res)
	if err != nil {
		common.SysError("failed to decode wechatCorpAccountResponse: " + err.Error())
		return
	}
	if res.ErrorCode != 0 {
		common.SysError(res.ErrorMessage)
		return
	}
	i.AccessToken = res.AccessToken
	common.SysLog("access token refreshed")
}

type wechatCorpMessageRequest struct {
	MessageType string `json:"msgtype"`
	ToUser      string `json:"touser"`
	AgentId     string `json:"agentid"`
	TextCard    struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		URL         string `json:"url"`
		Btntxt      string `json:"btntxt"`
	} `json:"textcard"`
	Text struct {
		Content string `json:"content"`
	} `json:"text"`
	Markdown struct {
		Content string `json:"content"`
	} `json:"markdown"`
	News struct {
		Articles []struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			URL         string `json:"url"`
			PicURL      string `json:"picurl"`
		} `json:"articles"`
	} `json:"news"`
	MpNews struct {
		Articles []struct {
			Title            string `json:"title"`
			ThumbMediaID     string `json:"thumb_media_id"`
			Author           string `json:"author"`
			ContentSourceURL string `json:"content_source_url"`
			Content          string `json:"content"`
			Digest           string `json:"digest"`
		} `json:"articles"`
	} `json:"mpnews"`
}

type wechatCorpMessageResponse struct {
	ErrorCode    int    `json:"errcode"`
	ErrorMessage string `json:"errmsg"`
}

func parseWechatCorpAccountAppId(appId string) (string, string, error) {
	parts := strings.Split(appId, "|")
	if len(parts) != 2 {
		return "", "", errors.New("无效的微信企业号配置")
	}
	return parts[0], parts[1], nil
}

func SendWeChatCorpMessage(message *model.Message, user *model.User, channel_ *model.Channel) error {
    if message == nil || user == nil || channel_ == nil {
        return errors.New("message, user or channel is nil")
    }
	// https://developer.work.weixin.com/document/path/90236
	corpId, agentId, err := parseWechatCorpAccountAppId(channel_.AppId)
	if err != nil {
		return err
	}
	userId := channel_.AccountId
	clientType := channel_.Other
	agentSecret := channel_.Secret
	messageRequest := wechatCorpMessageRequest{
		ToUser:  userId,
		AgentId: agentId,
	}
	if message.To != "" {
		messageRequest.ToUser = message.To
	}

	if clientType == "plugin" {
		// 按优先级和关键属性判断消息类型
		if len(message.Articles) > 0 {
			// 检查是否有 mpnews 所需的 content 字段来判断是否为 mpnews
			if message.Articles[0].Content != "" && message.Articles[0].ThumbMediaID != "" {
				messageRequest.MessageType = "mpnews"
				for _, article := range message.Articles {
					messageRequest.MpNews.Articles = append(messageRequest.MpNews.Articles, struct {
						Title            string `json:"title"`
						ThumbMediaID     string `json:"thumb_media_id"`
						Author           string `json:"author"`
						ContentSourceURL string `json:"content_source_url"`
						Content          string `json:"content"`
						Digest           string `json:"digest"`
					}{ // 移除 omitempty 标签，保持与 wechatCorpMessageRequest.MpNews.Articles 结构体一致
						Title:            article.Title,
						ThumbMediaID:     article.ThumbMediaID,
						Author:           article.Author,
						ContentSourceURL: article.ContentSourceURL,
						Content:          article.Content,
						Digest:           article.Digest,
					})
				}
			} else {
				// 否则判断为 news 类型
				messageRequest.MessageType = "news"
				for _, article := range message.Articles {
					messageRequest.News.Articles = append(messageRequest.News.Articles, struct {
						Title       string `json:"title"`
						Description string `json:"description"`
						URL         string `json:"url"`
						PicURL      string `json:"picurl"`
					}{
						Title:       article.Title,
						Description: article.Description,
						URL:         article.URL,
						PicURL:      article.PicURL,
					})
				}
			}
		} else if message.Title != "" {
			// textcard 消息判断：存在 title 属性
			messageRequest.MessageType = "textcard"
			messageRequest.TextCard.Title = message.Title
			messageRequest.TextCard.Description = message.Description
			messageRequest.TextCard.URL = message.URL
			messageRequest.TextCard.Btntxt = message.Btntxt
		} else if message.Content != "" {
			// text 消息判断：存在 content 属性
			messageRequest.MessageType = "text"
			messageRequest.Text.Content = message.Content
		}
	} else if message.Content != "" {
		// 非 plugin 客户端，使用 markdown 类型
		messageRequest.MessageType = "markdown"
		messageRequest.Markdown.Content = message.Content
	}

	jsonData, err := json.Marshal(messageRequest)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%s%s%s", corpId, agentId, agentSecret)
	accessToken := TokenStoreGetToken(key)
	resp, err := http.Post(fmt.Sprintf("https://qyapi.weixin.qq.com/cgi-bin/message/send?access_token=%s", accessToken), "application/json",
		bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	var res wechatCorpMessageResponse
	err = json.NewDecoder(resp.Body).Decode(&res)
	if err != nil {
		return err
	}
	if res.ErrorCode != 0 {
		return errors.New(res.ErrorMessage)
	}
	return nil
}
