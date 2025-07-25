package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"message-pusher/channel"
	"message-pusher/common"
	"message-pusher/model"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func keepCompatible(message *model.Message) {
	// Keep compatible with ServerChan: https://sct.ftqq.com/sendkey
	if message.Description == "" {
		message.Description = message.Short
	}
	if message.Content == "" {
		message.Content = message.Desp
	}
	if message.To == "" {
		message.To = message.OpenId
	}
}

// parseArticles 解析文章列表 JSON 字符串
func parseArticles(articlesStr string) []model.Article { 
    var articles []model.Article 
    if articlesStr != "" { 
        err := json.Unmarshal([]byte(articlesStr), &articles) 
        if err != nil { 
            common.SysError("解析 Articles 字段失败: " + err.Error()) 
        } 
    } 
    return articles 
} 

// GetPushMessage 处理 GET 请求，从查询参数中获取消息信息并推送消息
func GetPushMessage(c *gin.Context) { 
    message := model.Message{ 
        Title:       c.Query("title"),
        Description: c.Query("description"),
        Content:     c.Query("content"),
        URL:         c.Query("url"),
        Btntxt:      c.Query("btntxt"),
        Channel:     c.Query("channel"),
        Token:       c.Query("token"),
        To:          c.Query("to"),
        Desp:        c.Query("desp"),
        Short:       c.Query("short"),
        OpenId:      c.Query("openid"),
        Async:       c.Query("async") == "true",
        RenderMode:  c.Query("render_mode"),
        Articles:    parseArticles(c.Query("articles")), 
    } 
    keepCompatible(&message) 
    pushMessageHelper(c, &message) 
} 

// PostPushMessage 处理 POST 请求，从表单或 JSON 中获取消息信息并推送消息
func PostPushMessage(c *gin.Context) { 
    var message model.Message 
    if strings.Contains(strings.ToLower(c.Request.Header.Get("Content-Type")), "application/json") { 
        // 用户使用 JSON 格式请求 
        message = model.Message{} 
        err := json.NewDecoder(c.Request.Body).Decode(&message) 
        if err != nil { 
            c.JSON(http.StatusOK, gin.H{ 
                "success": false, 
                "message": "无法解析请求体，请检查其是否为合法 JSON", 
            }) 
            return 
        } 
    } else { 
        message = model.Message{ 
            Title:       c.PostForm("title"),
            Description: c.PostForm("description"),
            Content:     c.PostForm("content"),
            URL:         c.PostForm("url"),
            Btntxt:      c.PostForm("btntxt"),
            Channel:     c.PostForm("channel"),
            Token:       c.PostForm("token"),
            To:          c.PostForm("to"),
            Desp:        c.PostForm("desp"),
            Short:       c.PostForm("short"),
            OpenId:      c.PostForm("openid"),
            Async:       c.PostForm("async") == "true",
            RenderMode:  c.PostForm("render_mode"),
            Articles:    parseArticles(c.PostForm("articles")), 
        } 
    }
	// 修改比较逻辑，检查关键字段是否为空
	if message.Title == "" && message.Description == "" && message.Content == "" && message.Channel == "" && message.Token == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "请求体为空，如果使用 JSON 请设置 Content-Type 为 application/json，否则请使用表单提交",
		})
		return
	}
	if message.Token == "" {
		message.Token = c.Query("token")
	}
	keepCompatible(&message)
	pushMessageHelper(c, &message)
}

func pushMessageHelper(c *gin.Context, message *model.Message) {
	user := model.User{Username: c.Param("username")}
	err := user.FillUserByUsername()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	if user.Status == common.UserStatusNonExisted {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "用户不存在",
		})
		return
	}
	if user.Status == common.UserStatusDisabled {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "用户已被封禁",
		})
		return
	}
	if message.Token == "" {
		message.Token = strings.TrimPrefix(c.Request.Header.Get("Authorization"), "Bearer ")
	}
	processMessage(c, message, &user, true)
}

func authMessage(messageToken string, userToken string, channelToken *string) bool {
	if userToken != "" {
		if messageToken == userToken {
			return true
		}
	}
	if channelToken != nil && *channelToken != "" {
		if messageToken != *channelToken {
			return false
		}
	}
	return true
}

func processMessage(c *gin.Context, message *model.Message, user *model.User, needAuth bool) {
	if message.Title == "" {
		message.Title = common.SystemName
	}
	if message.Channel == "" {
		message.Channel = user.Channel
		if message.Channel == "" {
			message.Channel = model.TypeEmail
		}
	}
	channel_, err := model.GetChannelByName(message.Channel, user.Id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无效的渠道名称：" + message.Channel,
		})
		return
	}
	if needAuth && !authMessage(message.Token, user.Token, channel_.Token) {
		if message.Token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "通道维度或用户维度设置了鉴权令牌，需要提供鉴权令牌",
			})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "无效的 token",
		})
		return
	}
	if message.RenderMode == "code" {
		if message.Content != "" {
			message.Content = fmt.Sprintf("```\n%s\n```", message.Content)
		}
	}
	err = saveAndSendMessage(user, message, channel_)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"uuid":    message.Link,
	})
}

// saveAndSendMessage 保存消息并发送消息，根据配置决定是否持久化消息，同时处理消息同步和发送逻辑
func saveAndSendMessage(user *model.User, message *model.Message, channel_ *model.Channel) error { 
    if channel_.Status != common.ChannelStatusEnabled { 
        return errors.New("该渠道已被禁用") 
    } 
    common.MessageCount += 1 // We don't need to use atomic here because it's not a critical value 
    message.Link = common.GetUUID() 
    if message.URL == "" { 
        message.URL = fmt.Sprintf("%s/message/%s", common.ServerAddress, message.Link) 
    } 
    success := false 
    if common.MessagePersistenceEnabled || user.SaveMessageToDatabase == common.SaveMessageToDatabaseAllowed { 
        defer func() { 
            // Update the status of the message 
            status := common.MessageSendStatusFailed 
            if message.Async { 
                status = common.MessageSendStatusAsyncPending 
            } else { 
                if success { 
                    status = common.MessageSendStatusSent 
                } 
            } 
            err := message.UpdateStatus(status) 
            if err != nil { 
                common.SysError("failed to update the status of the message: " + err.Error()) 
            } 
            if message.Async { 
                channel.AsyncMessageQueue <- message.Id 
            } 
        }() 
        err := message.UpdateAndInsert(user.Id) 
        if err != nil { 
            common.SysError("保存消息失败: " + err.Error()) 
            return err 
        } 
        // 异步执行消息同步操作，并添加错误处理 
        go func() { 
            syncMessageToUser(message, user.Id)
        }() 
    } else { 
        if message.Async { 
            return errors.New("异步发送消息需要用户具备消息持久化的权限") 
        } 
        message.Link = "unsaved" // This is for user to identify whether the message is saved 
        // 修正：使用匿名函数包裹调用并添加错误处理 
        go func() { 
            syncMessageToUser(message, user.Id)
        }() 
    } 
    if !message.Async { 
        err := channel.SendMessage(message, user, channel_) 
        if err != nil { 
            common.SysError("发送消息失败: " + err.Error()) // 添加错误日志 
            return err 
        } 
    } 
    success = true 
    return nil // After this line, the message status will be updated 
} 

func GetStaticFile(c *gin.Context) {
	path := c.Param("file")
	c.FileFromFS("public/static/"+path, http.FS(common.FS))
}

func RenderMessage(c *gin.Context) {
	if !common.MessageRenderEnabled {
		c.HTML(http.StatusOK, "message.html", gin.H{
			"title":       "无法渲染",
			"time":        time.Now().Format("2006-01-02 15:04:05"),
			"description": "超级管理员禁用了消息渲染",
			"content":     "很抱歉，您所使用的消息推送服务的管理员禁用了消息渲染功能，因此您的消息无法渲染。",
		})
		return
	}
	link := c.Param("link")
	if link == "unsaved" {
		c.HTML(http.StatusOK, "message.html", gin.H{
			"title":       "无法渲染",
			"time":        time.Now().Format("2006-01-02 15:04:05"),
			"description": "超级管理员禁用了消息持久化",
			"content":     "很抱歉，您所使用的消息推送服务的管理员禁用了消息持久化功能，您的消息并没有存储到数据库中，因此无法渲染。",
		})
		return
	}
	message, err := model.GetMessageByLink(link)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	if message.RenderMode != "raw" {
		if message.Description != "" {
			message.Description, err = common.Markdown2HTML(message.Description)
			if err != nil {
				common.SysLog(err.Error())
			}
		}
		if message.Content != "" {
			message.HTMLContent, err = common.Markdown2HTML(message.Content)
			if err != nil {
				common.SysLog(err.Error())
			}
		}
	}
	c.HTML(http.StatusOK, "message.html", gin.H{
		"title":       message.Title,
		"time":        time.Unix(message.Timestamp, 0).Format("2006-01-02 15:04:05"),
		"description": message.Description,
		"content":     message.HTMLContent,
	})
	return
}

func GetUserMessages(c *gin.Context) {
	userId := c.GetInt("id")
	p, _ := strconv.Atoi(c.Query("p"))
	if p < 0 {
		p = 0
	}
	messages, err := model.GetMessagesByUserId(userId, p*common.ItemsPerPage, common.ItemsPerPage)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    messages,
	})
	return
}

func GetMessage(c *gin.Context) {
	messageId, _ := strconv.Atoi(c.Param("id"))
	userId := c.GetInt("id")
	message, err := model.GetMessageByIds(messageId, userId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    message,
	})
	return
}

func GetMessageStatus(c *gin.Context) {
	link := c.Param("link")
	status, err := model.GetMessageStatusByLink(link)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"status":  status,
	})
	return
}

func SearchMessages(c *gin.Context) {
	keyword := c.Query("keyword")
	messages, err := model.SearchMessages(keyword)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    messages,
	})
	return
}

func ResendMessage(c *gin.Context) {
	messageId, _ := strconv.Atoi(c.Param("id"))
	userId := c.GetInt("id")
	helper := func() error {
		message, err := model.GetMessageByIds(messageId, userId)
		message.Id = 0
		if err != nil {
			return err
		}
		user, err := model.GetUserById(userId, true)
		if err != nil {
			return err
		}
		channel_, err := model.GetChannelByName(message.Channel, user.Id)
		if err != nil {
			return err
		}
		err = saveAndSendMessage(user, message, channel_)
		if err != nil {
			return err
		}
		return nil
	}
	err := helper()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func DeleteMessage(c *gin.Context) {
	messageId, _ := strconv.Atoi(c.Param("id"))
	userId := c.GetInt("id")
	err := model.DeleteMessageById(messageId, userId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func DeleteAllMessages(c *gin.Context) {
	err := model.DeleteAllMessages()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}
