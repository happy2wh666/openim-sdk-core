package conversation_msg

import (
	"context"
	"fmt"
	"github.com/jinzhu/copier"
	"github.com/openimsdk/openim-sdk-core/v3/internal/third/file"
	"github.com/openimsdk/openim-sdk-core/v3/pkg/content_type"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openimsdk/tools/errs"

	"github.com/openimsdk/openim-sdk-core/v3/open_im_sdk_callback"
	"github.com/openimsdk/openim-sdk-core/v3/pkg/common"
	"github.com/openimsdk/openim-sdk-core/v3/pkg/constant"
	"github.com/openimsdk/openim-sdk-core/v3/pkg/db/model_struct"
	"github.com/openimsdk/openim-sdk-core/v3/pkg/sdk_params_callback"
	"github.com/openimsdk/openim-sdk-core/v3/pkg/sdkerrs"
	"github.com/openimsdk/openim-sdk-core/v3/pkg/server_api_params"
	"github.com/openimsdk/openim-sdk-core/v3/pkg/utils"
	"github.com/openimsdk/openim-sdk-core/v3/sdk_struct"

	"github.com/openimsdk/tools/log"

	pbConversation "github.com/openimsdk/protocol/conversation"
	"github.com/openimsdk/protocol/sdkws"
)

func (c *Conversation) GetAllConversationList(ctx context.Context) ([]*model_struct.LocalConversation, error) {
	return c.db.GetAllConversationListDB(ctx)
}

func (c *Conversation) GetConversationListSplit(ctx context.Context, offset, count int) ([]*model_struct.LocalConversation, error) {
	return c.db.GetConversationListSplitDB(ctx, offset, count)
}

func (c *Conversation) HideConversation(ctx context.Context, conversationID string) error {
	err := c.db.ResetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	return nil
}

func (c *Conversation) GetAtAllTag(_ context.Context) string {
	return constant.AtAllString
}

func (c *Conversation) GetOneConversation(ctx context.Context, sessionType int32, sourceID string) (*model_struct.LocalConversation, error) {
	// 生成会话ID并记录调试日志，包含ZZWWZZWWZZ标识
	conversationID := c.getConversationIDBySessionType(sourceID, int(sessionType))
	log.ZDebug(ctx, "ZZWWZZWWZZ 生成会话ID",
		"sessionType", sessionType,
		"sourceID", sourceID,
		"conversationID", conversationID)

	// 尝试从数据库获取会话
	lc, err := c.db.GetConversation(ctx, conversationID)
	if err == nil {
		log.ZDebug(ctx, "ZZWWZZWWZZ 成功从数据库获取会话",
			"conversationID", conversationID,
			"conversation", lc)
		return lc, nil
	}

	// 首次获取失败，记录错误并准备创建新会话
	log.ZDebug(ctx, "ZZWWZZWWZZ 首次获取会话失败，准备创建新会话",
		"conversationID", conversationID,
		"error", err)

	var newConversation model_struct.LocalConversation
	newConversation.ConversationID = conversationID
	newConversation.ConversationType = sessionType

	// 根据会话类型设置不同属性
	switch sessionType {
	case constant.SingleChatType:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理单聊类型会话",
			"sessionType", sessionType,
			"sourceID", sourceID)

		newConversation.UserID = sourceID
		faceUrl, name, err := c.getUserNameAndFaceURL(ctx, sourceID)
		if err != nil {
			log.ZDebug(ctx, "ZZWWZZWWZZ 获取用户信息失败",
				"sourceID", sourceID,
				"error", err)
			return nil, err
		}
		newConversation.ShowName = name
		newConversation.FaceURL = faceUrl
		log.ZDebug(ctx, "ZZWWZZWWZZ 单聊会话信息设置完成",
			"userID", sourceID,
			"showName", name,
			"faceUrl", faceUrl)

	case constant.WriteGroupChatType, constant.ReadGroupChatType:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理群聊类型会话",
			"sessionType", sessionType,
			"sourceID", sourceID)

		newConversation.GroupID = sourceID
		g, err := c.group.FetchGroupOrError(ctx, sourceID)
		if err != nil {
			log.ZDebug(ctx, "ZZWWZZWWZZ 获取群组信息失败",
				"groupID", sourceID,
				"error", err)
			return nil, err
		}
		newConversation.ShowName = g.GroupName
		newConversation.FaceURL = g.FaceURL
		log.ZDebug(ctx, "ZZWWZZWWZZ 群聊会话信息设置完成",
			"groupID", sourceID,
			"showName", g.GroupName,
			"faceUrl", g.FaceURL)
	}

	// 双重检查会话是否已存在
	log.ZDebug(ctx, "ZZWWZZWWZZ 执行双重检查，确认会话是否存在",
		"conversationID", conversationID)
	lc, err = c.db.GetConversation(ctx, conversationID)
	if err == nil {
		log.ZDebug(ctx, "ZZWWZZWWZZ 双重检查发现会话已存在，返回数据库中的会话",
			"conversationID", conversationID,
			"conversation", lc)
		return lc, nil
	}

	// 返回新创建的会话
	log.ZDebug(ctx, "ZZWWZZWWZZ 双重检查确认会话不存在，返回新创建的会话",
		"conversationID", conversationID,
		"newConversation", newConversation)
	return &newConversation, nil
}

func (c *Conversation) GetMultipleConversation(ctx context.Context, conversationIDList []string) ([]*model_struct.LocalConversation, error) {
	conversations, err := c.db.GetMultipleConversationDB(ctx, conversationIDList)
	if err != nil {
		return nil, err
	}
	return conversations, nil

}

func (c *Conversation) HideAllConversations(ctx context.Context) error {
	err := c.db.ResetAllConversation(ctx)
	if err != nil {
		return err
	}
	return nil
}

func (c *Conversation) SetConversationDraft(ctx context.Context, conversationID, draftText string) error {
	if draftText != "" {
		err := c.db.SetConversationDraftDB(ctx, conversationID, draftText)
		if err != nil {
			return err
		}
	} else {
		err := c.db.RemoveConversationDraft(ctx, conversationID, draftText)
		if err != nil {
			return err
		}
	}
	_ = common.TriggerCmdUpdateConversation(ctx, common.UpdateConNode{Action: constant.ConChange, Args: []string{conversationID}}, c.GetCh())
	return nil
}

func (c *Conversation) SetConversation(ctx context.Context, conversationID string, req *pbConversation.ConversationReq) error {
	c.conversationSyncMutex.Lock()
	defer c.conversationSyncMutex.Unlock()

	lc, err := c.db.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	apiReq := &pbConversation.SetConversationsReq{Conversation: req}
	err = c.setConversation(ctx, apiReq, lc)
	if err != nil {
		return err
	}
	return c.IncrSyncConversations(ctx)
}

func (c *Conversation) GetTotalUnreadMsgCount(ctx context.Context) (totalUnreadCount int32, err error) {
	return c.db.GetTotalUnreadMsgCountDB(ctx)
}

func (c *Conversation) SetConversationListener(listener func() open_im_sdk_callback.OnConversationListener) {
	c.ConversationListener = listener
}

func (c *Conversation) updateMsgStatusAndTriggerConversation(ctx context.Context, clientMsgID, serverMsgID string, sendTime int64, status int32, s *sdk_struct.MsgStruct,
	lc *model_struct.LocalConversation, isOnlineOnly bool) {
	log.ZDebug(ctx, "this is test send message ", "sendTime", sendTime, "status", status, "clientMsgID", clientMsgID, "serverMsgID", serverMsgID)
	if isOnlineOnly {
		return
	}
	s.SendTime = sendTime
	s.Status = status
	s.ServerMsgID = serverMsgID
	err := c.db.UpdateMessageTimeAndStatus(ctx, lc.ConversationID, clientMsgID, serverMsgID, sendTime, status)
	if err != nil {
		log.ZWarn(ctx, "send message update message status error", err,
			"sendTime", sendTime, "status", status, "clientMsgID", clientMsgID, "serverMsgID", serverMsgID)
	}
	err = c.db.DeleteSendingMessage(ctx, lc.ConversationID, clientMsgID)
	if err != nil {
		log.ZWarn(ctx, "send message delete sending message error", err)
	}
	lc.LatestMsg = utils.StructToJsonString(s)
	lc.LatestMsgSendTime = sendTime
	_ = common.TriggerCmdUpdateConversation(ctx, common.UpdateConNode{ConID: lc.ConversationID, Action: constant.AddConOrUpLatMsg, Args: *lc}, c.GetCh())
}

func (c *Conversation) fileName(ftype string, id string) string {
	return fmt.Sprintf("msg_%s_%s", ftype, id)
}

func (c *Conversation) checkID(ctx context.Context, s *sdk_struct.MsgStruct,
	recvID, groupID string, options map[string]bool) (*model_struct.LocalConversation, error) {
	if recvID == "" && groupID == "" {
		return nil, sdkerrs.ErrArgs
	}
	s.SendID = c.loginUserID
	s.SenderPlatformID = c.platformID
	lc := &model_struct.LocalConversation{LatestMsgSendTime: s.CreateTime}
	//assemble messages and conversations based on single or group chat types
	if recvID == "" {
		g, err := c.group.FetchGroupOrError(ctx, groupID)
		if err != nil {
			return nil, err
		}
		lc.ShowName = g.GroupName
		lc.FaceURL = g.FaceURL
		switch g.GroupType {
		case constant.NormalGroup:
			s.SessionType = constant.WriteGroupChatType
			lc.ConversationType = constant.WriteGroupChatType
			lc.ConversationID = c.getConversationIDBySessionType(groupID, constant.WriteGroupChatType)
		case constant.SuperGroup, constant.WorkingGroup:
			s.SessionType = constant.ReadGroupChatType
			lc.ConversationID = c.getConversationIDBySessionType(groupID, constant.ReadGroupChatType)
			lc.ConversationType = constant.ReadGroupChatType
		}
		s.GroupID = groupID
		lc.GroupID = groupID
		gm, err := c.db.GetGroupMemberInfoByGroupIDUserID(ctx, groupID, c.loginUserID)
		if err == nil && gm != nil {
			if gm.Nickname != "" {
				s.SenderNickname = gm.Nickname
			}
		} else { //Maybe the group member information hasn't been pulled locally yet.
			gm, err := c.group.GetSpecifiedGroupMembersInfo(ctx, groupID, []string{c.loginUserID})
			if err == nil && gm != nil {
				if gm[0].Nickname != "" {
					s.SenderNickname = gm[0].Nickname
				}
			}
		}
		var attachedInfo sdk_struct.AttachedInfoElem
		attachedInfo.GroupHasReadInfo.GroupMemberCount = g.MemberCount
		s.AttachedInfoElem = &attachedInfo
	} else {
		s.SessionType = constant.SingleChatType
		s.RecvID = recvID
		lc.ConversationID = utils.GetConversationIDByMsg(s)
		lc.UserID = recvID
		lc.ConversationType = constant.SingleChatType
		oldLc, err := c.db.GetConversation(ctx, lc.ConversationID)
		if err == nil && oldLc.IsPrivateChat {
			options[constant.IsNotPrivate] = false
			var attachedInfo sdk_struct.AttachedInfoElem
			attachedInfo.IsPrivateChat = true
			attachedInfo.BurnDuration = oldLc.BurnDuration
			s.AttachedInfoElem = &attachedInfo
		}
		if err != nil {
			t := time.Now()
			faceUrl, name, err := c.getUserNameAndFaceURL(ctx, recvID)
			log.ZDebug(ctx, "GetUserNameAndFaceURL", "cost time", time.Since(t))
			if err != nil {
				return nil, err
			}
			lc.FaceURL = faceUrl
			lc.ShowName = name
		}

	}
	return lc, nil
}
func (c *Conversation) getConversationIDBySessionType(sourceID string, sessionType int) string {
	switch sessionType {
	case constant.SingleChatType:
		l := []string{c.loginUserID, sourceID}
		sort.Strings(l)
		return "si_" + strings.Join(l, "_") // single chat
	case constant.WriteGroupChatType:
		return "g_" + sourceID // group chat
	case constant.ReadGroupChatType:
		return "sg_" + sourceID // super group chat
	case constant.NotificationChatType:
		return "sn_" + sourceID + "_" + c.loginUserID // server notification chat
	}
	return ""
}

func (c *Conversation) GetConversationIDBySessionType(_ context.Context, sourceID string, sessionType int) string {
	return c.getConversationIDBySessionType(sourceID, sessionType)
}

func (c *Conversation) SendMessage(ctx context.Context, s *sdk_struct.MsgStruct, recvID, groupID string, p *sdkws.OfflinePushInfo, isOnlineOnly bool) (*sdk_struct.MsgStruct, error) {
	log.ZDebug(ctx, "ZZWWZZWWZZ 开始处理消息发送",
		"clientMsgID", s.ClientMsgID,
		"contentType", s.ContentType,
		"recvID", recvID,
		"groupID", groupID,
		"isOnlineOnly", isOnlineOnly)

	// 定义文件路径处理函数
	filepathExt := func(name ...string) string {
		for _, path := range name {
			if ext := filepath.Ext(path); ext != "" {
				return ext
			}
		}
		return ""
	}
	options := make(map[string]bool, 2)

	// 检查ID合法性并获取会话信息
	log.ZDebug(ctx, "ZZWWZZWWZZ 开始检查消息接收者ID合法性",
		"clientMsgID", s.ClientMsgID)
	lc, err := c.checkID(ctx, s, recvID, groupID, options)
	if err != nil {
		log.ZError(ctx, "ZZWWZZWWZZ 检查ID合法性失败", err,
			"clientMsgID", s.ClientMsgID,
			"recvID", recvID,
			"groupID", groupID)
		return nil, err
	}
	log.ZDebug(ctx, "ZZWWZZWWZZ ID合法性检查通过，获取会话信息",
		"clientMsgID", s.ClientMsgID,
		"conversationID", lc.ConversationID,
		"conversationType", lc.ConversationType)

	// 获取发送回调
	callback, _ := ctx.Value("callback").(open_im_sdk_callback.SendMsgCallBack)
	log.ZDebug(ctx, "ZZWWZZWWZZ 准备处理本地消息存储",
		"clientMsgID", s.ClientMsgID,
		"isOnlineOnly", isOnlineOnly)

	// 处理本地消息存储
	if !isOnlineOnly {
		oldMessage, err := c.db.GetMessage(ctx, lc.ConversationID, s.ClientMsgID)
		if err != nil {
			log.ZDebug(ctx, "ZZWWZZWWZZ 本地不存在该消息，准备插入新消息",
				"clientMsgID", s.ClientMsgID,
				"conversationID", lc.ConversationID)
			localMessage := MsgStructToLocalChatLog(s)
			err := c.db.InsertMessage(ctx, lc.ConversationID, localMessage)
			if err != nil {
				log.ZError(ctx, "ZZWWZZWWZZ 插入消息到本地数据库失败", err,
					"clientMsgID", s.ClientMsgID,
					"conversationID", lc.ConversationID)
				return nil, err
			}
			log.ZDebug(ctx, "ZZWWZZWWZZ 消息成功插入本地数据库",
				"clientMsgID", s.ClientMsgID,
				"conversationID", lc.ConversationID)

			err = c.db.InsertSendingMessage(ctx, &model_struct.LocalSendingMessages{
				ConversationID: lc.ConversationID,
				ClientMsgID:    localMessage.ClientMsgID,
			})
			if err != nil {
				log.ZError(ctx, "ZZWWZZWWZZ 插入发送中消息记录失败", err,
					"clientMsgID", s.ClientMsgID,
					"conversationID", lc.ConversationID)
				return nil, err
			}
			log.ZDebug(ctx, "ZZWWZZWWZZ 发送中消息记录插入成功",
				"clientMsgID", s.ClientMsgID)
		} else {
			if oldMessage.Status != constant.MsgStatusSendFailed {
				log.ZDebug(ctx, "ZZWWZZWWZZ 消息已存在且状态不为发送失败，拒绝重复发送",
					"clientMsgID", s.ClientMsgID,
					"currentStatus", oldMessage.Status)
				return nil, sdkerrs.ErrMsgRepeated
			} else {
				log.ZDebug(ctx, "ZZWWZZWWZZ 消息存在但状态为发送失败，重新发送",
					"clientMsgID", s.ClientMsgID)
				s.Status = constant.MsgStatusSending
				err = c.db.InsertSendingMessage(ctx, &model_struct.LocalSendingMessages{
					ConversationID: lc.ConversationID,
					ClientMsgID:    s.ClientMsgID,
				})
				if err != nil {
					log.ZError(ctx, "ZZWWZZWWZZ 重新插入发送中消息记录失败", err,
						"clientMsgID", s.ClientMsgID)
					return nil, err
				}
			}
		}

		// 更新会话最新消息
		lc.LatestMsg = utils.StructToJsonString(s)
		log.ZDebug(ctx, "ZZWWZZWWZZ 准备触发会话更新",
			"conversationID", lc.ConversationID,
			"clientMsgID", s.ClientMsgID)
		_ = common.TriggerCmdUpdateConversation(ctx, common.UpdateConNode{
			ConID:  lc.ConversationID,
			Action: constant.AddConOrUpLatMsg,
			Args:   *lc,
		}, c.GetCh())
	}

	var delFile []string
	// 处理媒体文件
	log.ZDebug(ctx, "ZZWWZZWWZZ 开始处理媒体文件",
		"clientMsgID", s.ClientMsgID,
		"contentType", s.ContentType)
	switch s.ContentType {
	case constant.Picture:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理图片消息",
			"clientMsgID", s.ClientMsgID,
			"sourcePath", s.PictureElem.SourcePath)
		if s.Status == constant.MsgStatusSendSuccess {
			s.Content = utils.StructToJsonString(s.PictureElem)
			log.ZDebug(ctx, "ZZWWZZWWZZ 图片消息状态为已发送，跳过上传",
				"clientMsgID", s.ClientMsgID)
			break
		}

		// 处理图片路径
		var sourcePath string
		if utils.FileExist(s.PictureElem.SourcePath) {
			sourcePath = s.PictureElem.SourcePath
			delFile = append(delFile, utils.FileTmpPath(s.PictureElem.SourcePath, c.DataDir))
		} else {
			sourcePath = utils.FileTmpPath(s.PictureElem.SourcePath, c.DataDir)
			delFile = append(delFile, sourcePath)
		}
		log.ZDebug(ctx, "ZZWWZZWWZZ 确定图片上传路径",
			"sourcePath", sourcePath,
			"tempFilesToDelete", delFile)

		// 上传图片
		res, err := c.file.UploadFile(ctx, &file.UploadFileReq{
			ContentType: s.PictureElem.SourcePicture.Type,
			Filepath:    sourcePath,
			Uuid:        s.PictureElem.SourcePicture.UUID,
			Name:        c.fileName("picture", s.ClientMsgID) + filepathExt(s.PictureElem.SourcePicture.UUID, sourcePath),
			Cause:       "msg-picture",
		}, NewUploadFileCallback(ctx, callback.OnProgress, s, lc.ConversationID, c.db))
		if err != nil {
			log.ZError(ctx, "ZZWWZZWWZZ 图片上传失败", err,
				"clientMsgID", s.ClientMsgID,
				"sourcePath", sourcePath)
			c.updateMsgStatusAndTriggerConversation(ctx, s.ClientMsgID, "", s.CreateTime, constant.MsgStatusSendFailed, s, lc, isOnlineOnly)
			return nil, err
		}
		log.ZDebug(ctx, "ZZWWZZWWZZ 图片上传成功",
			"clientMsgID", s.ClientMsgID,
			"url", res.URL)

		// 处理图片URL和缩略图
		s.PictureElem.SourcePicture.Url = res.URL
		s.PictureElem.BigPicture = s.PictureElem.SourcePicture
		u, err := url.Parse(res.URL)
		if err == nil {
			snapshot := u.Query()
			snapshot.Set("type", "image")
			snapshot.Set("width", "640")
			snapshot.Set("height", "640")
			u.RawQuery = snapshot.Encode()
			s.PictureElem.SnapshotPicture = &sdk_struct.PictureBaseInfo{
				Width:  640,
				Height: 640,
				Url:    u.String(),
			}
			log.ZDebug(ctx, "ZZWWZZWWZZ 生成图片缩略图URL",
				"clientMsgID", s.ClientMsgID,
				"snapshotUrl", u.String())
		} else {
			log.ZError(ctx, "ZZWWZZWWZZ 解析图片URL失败，使用原图URL作为缩略图", err,
				"clientMsgID", s.ClientMsgID,
				"url", res.URL)
			s.PictureElem.SnapshotPicture = s.PictureElem.SourcePicture
		}
		s.Content = utils.StructToJsonString(s.PictureElem)

	case constant.Sound:
		// 音频消息处理（与图片逻辑类似，省略重复注释）
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理音频消息",
			"clientMsgID", s.ClientMsgID,
			"soundPath", s.SoundElem.SoundPath)
		if s.Status == constant.MsgStatusSendSuccess {
			s.Content = utils.StructToJsonString(s.SoundElem)
			log.ZDebug(ctx, "ZZWWZZWWZZ 音频消息状态为已发送，跳过上传",
				"clientMsgID", s.ClientMsgID)
			break
		}

		var sourcePath string
		if utils.FileExist(s.SoundElem.SoundPath) {
			sourcePath = s.SoundElem.SoundPath
			delFile = append(delFile, utils.FileTmpPath(s.SoundElem.SoundPath, c.DataDir))
		} else {
			sourcePath = utils.FileTmpPath(s.SoundElem.SoundPath, c.DataDir)
			delFile = append(delFile, sourcePath)
		}
		log.ZDebug(ctx, "ZZWWZZWWZZ 确定音频上传路径",
			"sourcePath", sourcePath,
			"tempFilesToDelete", delFile)

		res, err := c.file.UploadFile(ctx, &file.UploadFileReq{
			ContentType: s.SoundElem.SoundType,
			Filepath:    sourcePath,
			Uuid:        s.SoundElem.UUID,
			Name:        c.fileName("voice", s.ClientMsgID) + filepathExt(s.SoundElem.UUID, sourcePath),
			Cause:       "msg-voice",
		}, NewUploadFileCallback(ctx, callback.OnProgress, s, lc.ConversationID, c.db))
		if err != nil {
			log.ZError(ctx, "ZZWWZZWWZZ 音频上传失败", err,
				"clientMsgID", s.ClientMsgID,
				"sourcePath", sourcePath)
			c.updateMsgStatusAndTriggerConversation(ctx, s.ClientMsgID, "", s.CreateTime, constant.MsgStatusSendFailed, s, lc, isOnlineOnly)
			return nil, err
		}
		log.ZDebug(ctx, "ZZWWZZWWZZ 音频上传成功",
			"clientMsgID", s.ClientMsgID,
			"url", res.URL)
		s.SoundElem.SourceURL = res.URL
		s.Content = utils.StructToJsonString(s.SoundElem)

	case constant.Video:
		// 视频消息处理（包含视频和缩略图并行上传）
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理视频消息",
			"clientMsgID", s.ClientMsgID,
			"videoPath", s.VideoElem.VideoPath,
			"snapshotPath", s.VideoElem.SnapshotPath)
		if s.Status == constant.MsgStatusSendSuccess {
			s.Content = utils.StructToJsonString(s.VideoElem)
			log.ZDebug(ctx, "ZZWWZZWWZZ 视频消息状态为已发送，跳过上传",
				"clientMsgID", s.ClientMsgID)
			break
		}

		var videoPath, snapPath string
		if utils.FileExist(s.VideoElem.VideoPath) {
			videoPath = s.VideoElem.VideoPath
			snapPath = s.VideoElem.SnapshotPath
			delFile = append(delFile, utils.FileTmpPath(s.VideoElem.VideoPath, c.DataDir))
			delFile = append(delFile, utils.FileTmpPath(s.VideoElem.SnapshotPath, c.DataDir))
		} else {
			videoPath = utils.FileTmpPath(s.VideoElem.VideoPath, c.DataDir)
			snapPath = utils.FileTmpPath(s.VideoElem.SnapshotPath, c.DataDir)
			delFile = append(delFile, videoPath, snapPath)
		}
		log.ZDebug(ctx, "ZZWWZZWWZZ 确定视频上传路径",
			"videoPath", videoPath,
			"snapPath", snapPath,
			"tempFilesToDelete", delFile)

		var wg sync.WaitGroup
		wg.Add(2)
		var putErrs error

		// 上传视频缩略图
		go func() {
			defer wg.Done()
			log.ZDebug(ctx, "ZZWWZZWWZZ 开始上传视频缩略图",
				"clientMsgID", s.ClientMsgID,
				"snapPath", snapPath)
			snapRes, err := c.file.UploadFile(ctx, &file.UploadFileReq{
				ContentType: s.VideoElem.SnapshotType,
				Filepath:    snapPath,
				Uuid:        s.VideoElem.SnapshotUUID,
				Name:        c.fileName("videoSnapshot", s.ClientMsgID) + filepathExt(s.VideoElem.SnapshotUUID, snapPath),
				Cause:       "msg-video-snapshot",
			}, nil)
			if err != nil {
				log.ZError(ctx, "ZZWWZZWWZZ 视频缩略图上传失败", err,
					"clientMsgID", s.ClientMsgID,
					"snapPath", snapPath)
				return
			}
			s.VideoElem.SnapshotURL = snapRes.URL
			log.ZDebug(ctx, "ZZWWZZWWZZ 视频缩略图上传成功",
				"clientMsgID", s.ClientMsgID,
				"snapshotUrl", snapRes.URL)
		}()

		// 上传视频文件
		go func() {
			defer wg.Done()
			log.ZDebug(ctx, "ZZWWZZWWZZ 开始上传视频文件",
				"clientMsgID", s.ClientMsgID,
				"videoPath", videoPath)
			res, err := c.file.UploadFile(ctx, &file.UploadFileReq{
				ContentType: content_type.GetType(s.VideoElem.VideoType, filepath.Ext(s.VideoElem.VideoPath)),
				Filepath:    videoPath,
				Uuid:        s.VideoElem.VideoUUID,
				Name:        c.fileName("video", s.ClientMsgID) + filepathExt(s.VideoElem.VideoUUID, videoPath),
				Cause:       "msg-video",
			}, NewUploadFileCallback(ctx, callback.OnProgress, s, lc.ConversationID, c.db))
			if err != nil {
				log.ZError(ctx, "ZZWWZZWWZZ 视频文件上传失败", err,
					"clientMsgID", s.ClientMsgID,
					"videoPath", videoPath)
				c.updateMsgStatusAndTriggerConversation(ctx, s.ClientMsgID, "", s.CreateTime, constant.MsgStatusSendFailed, s, lc, isOnlineOnly)
				putErrs = err
				return
			}
			if res != nil {
				s.VideoElem.VideoURL = res.URL
				log.ZDebug(ctx, "ZZWWZZWWZZ 视频文件上传成功",
					"clientMsgID", s.ClientMsgID,
					"videoUrl", res.URL)
			}
		}()

		wg.Wait()
		if err := putErrs; err != nil {
			return nil, err
		}
		s.Content = utils.StructToJsonString(s.VideoElem)

	case constant.File:
		// 文件消息处理（与图片逻辑类似，省略重复注释）
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理文件消息",
			"clientMsgID", s.ClientMsgID,
			"filePath", s.FileElem.FilePath)
		if s.Status == constant.MsgStatusSendSuccess {
			s.Content = utils.StructToJsonString(s.FileElem)
			log.ZDebug(ctx, "ZZWWZZWWZZ 文件消息状态为已发送，跳过上传",
				"clientMsgID", s.ClientMsgID)
			break
		}

		name := s.FileElem.FileName
		if name == "" {
			name = s.FileElem.FilePath
		}
		if name == "" {
			name = fmt.Sprintf("msg_file_%s.unknown", s.ClientMsgID)
		}

		var sourcePath string
		if utils.FileExist(s.FileElem.FilePath) {
			sourcePath = s.FileElem.FilePath
			delFile = append(delFile, utils.FileTmpPath(s.FileElem.FilePath, c.DataDir))
		} else {
			sourcePath = utils.FileTmpPath(s.FileElem.FilePath, c.DataDir)
			delFile = append(delFile, sourcePath)
		}
		log.ZDebug(ctx, "ZZWWZZWWZZ 确定文件上传路径",
			"sourcePath", sourcePath,
			"fileName", name,
			"tempFilesToDelete", delFile)

		res, err := c.file.UploadFile(ctx, &file.UploadFileReq{
			ContentType: content_type.GetType(s.FileElem.FileType, filepath.Ext(s.FileElem.FilePath), filepath.Ext(s.FileElem.FileName)),
			Filepath:    sourcePath,
			Uuid:        s.FileElem.UUID,
			Name:        c.fileName("file", s.ClientMsgID) + "/" + filepath.Base(name),
			Cause:       "msg-file",
		}, NewUploadFileCallback(ctx, callback.OnProgress, s, lc.ConversationID, c.db))
		if err != nil {
			log.ZError(ctx, "ZZWWZZWWZZ 文件上传失败", err,
				"clientMsgID", s.ClientMsgID,
				"sourcePath", sourcePath)
			c.updateMsgStatusAndTriggerConversation(ctx, s.ClientMsgID, "", s.CreateTime, constant.MsgStatusSendFailed, s, lc, isOnlineOnly)
			return nil, err
		}
		log.ZDebug(ctx, "ZZWWZZWWZZ 文件上传成功",
			"clientMsgID", s.ClientMsgID,
			"url", res.URL)
		s.FileElem.SourceURL = res.URL
		s.Content = utils.StructToJsonString(s.FileElem)

	// 文本及其他类型消息处理
	case constant.Text:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理文本消息", "clientMsgID", s.ClientMsgID)
		s.Content = utils.StructToJsonString(s.TextElem)
	case constant.AtText:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理@文本消息", "clientMsgID", s.ClientMsgID)
		s.Content = utils.StructToJsonString(s.AtTextElem)
	case constant.Location:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理位置消息", "clientMsgID", s.ClientMsgID)
		s.Content = utils.StructToJsonString(s.LocationElem)
	case constant.Custom:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理自定义消息", "clientMsgID", s.ClientMsgID)
		s.Content = utils.StructToJsonString(s.CustomElem)
	case constant.Merger:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理合并消息", "clientMsgID", s.ClientMsgID)
		s.Content = utils.StructToJsonString(s.MergeElem)
	case constant.Quote:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理引用消息",
			"clientMsgID", s.ClientMsgID,
			"quoteClientMsgID", s.QuoteElem.QuoteMessage.ClientMsgID)
		quoteMessage, err := c.db.GetMessage(ctx, lc.ConversationID, s.QuoteElem.QuoteMessage.ClientMsgID)
		if err != nil {
			log.ZWarn(ctx, "ZZWWZZWWZZ 引用的消息未找到", err,
				"clientMsgID", s.ClientMsgID,
				"quoteClientMsgID", s.QuoteElem.QuoteMessage.ClientMsgID)
		} else {
			s.QuoteElem.QuoteMessage.Seq = quoteMessage.Seq
		}
		s.Content = utils.StructToJsonString(s.QuoteElem)
	case constant.Card:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理名片消息", "clientMsgID", s.ClientMsgID)
		s.Content = utils.StructToJsonString(s.CardElem)
	case constant.Face:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理表情消息", "clientMsgID", s.ClientMsgID)
		s.Content = utils.StructToJsonString(s.FaceElem)
	case constant.AdvancedText:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理高级文本消息", "clientMsgID", s.ClientMsgID)
		s.Content = utils.StructToJsonString(s.AdvancedTextElem)
	default:
		log.ZError(ctx, "ZZWWZZWWZZ 不支持的消息类型", nil,
			"clientMsgID", s.ClientMsgID,
			"contentType", s.ContentType)
		return nil, sdkerrs.ErrMsgContentTypeNotSupport
	}

	// 更新媒体消息的本地存储
	if utils.IsContainInt(int(s.ContentType), []int{constant.Picture, constant.Sound, constant.Video, constant.File}) {
		if !isOnlineOnly {
			localMessage := MsgStructToLocalChatLog(s)
			log.ZDebug(ctx, "ZZWWZZWWZZ 更新本地媒体消息记录",
				"clientMsgID", s.ClientMsgID,
				"conversationID", lc.ConversationID)
			err = c.db.UpdateMessage(ctx, lc.ConversationID, localMessage)
			if err != nil {
				log.ZError(ctx, "ZZWWZZWWZZ 更新本地媒体消息失败", err,
					"clientMsgID", s.ClientMsgID)
				return nil, err
			}
		}
	}

	// 调用发送到服务器的方法
	log.ZDebug(ctx, "ZZWWZZWWZZ 准备将消息发送到服务器",
		"clientMsgID", s.ClientMsgID)
	return c.sendMessageToServer(ctx, s, lc, callback, delFile, p, options, isOnlineOnly)
}

func (c *Conversation) SendMessageNotOss(ctx context.Context, s *sdk_struct.MsgStruct, recvID, groupID string,
	p *sdkws.OfflinePushInfo, isOnlineOnly bool) (*sdk_struct.MsgStruct, error) {
	log.ZDebug(ctx, "ZZWWZZWWZZ 开始处理非OSS消息发送",
		"clientMsgID", s.ClientMsgID,
		"contentType", s.ContentType,
		"recvID", recvID,
		"groupID", groupID,
		"isOnlineOnly", isOnlineOnly)

	options := make(map[string]bool, 2)

	// 检查ID合法性并获取会话信息
	log.ZDebug(ctx, "ZZWWZZWWZZ 开始检查消息接收者ID合法性",
		"clientMsgID", s.ClientMsgID)
	lc, err := c.checkID(ctx, s, recvID, groupID, options)
	if err != nil {
		log.ZError(ctx, "ZZWWZZWWZZ 检查ID合法性失败", err,
			"clientMsgID", s.ClientMsgID,
			"recvID", recvID,
			"groupID", groupID)
		return nil, err
	}
	log.ZDebug(ctx, "ZZWWZZWWZZ ID合法性检查通过，获取会话信息",
		"clientMsgID", s.ClientMsgID,
		"conversationID", lc.ConversationID,
		"conversationType", lc.ConversationType)

	// 获取发送回调
	callback, _ := ctx.Value("callback").(open_im_sdk_callback.SendMsgCallBack)
	log.ZDebug(ctx, "ZZWWZZWWZZ 准备处理本地消息存储",
		"clientMsgID", s.ClientMsgID,
		"isOnlineOnly", isOnlineOnly)

	// 处理本地消息存储
	if !isOnlineOnly {
		oldMessage, err := c.db.GetMessage(ctx, lc.ConversationID, s.ClientMsgID)
		if err != nil {
			log.ZDebug(ctx, "ZZWWZZWWZZ 本地不存在该消息，准备插入新消息",
				"clientMsgID", s.ClientMsgID,
				"conversationID", lc.ConversationID)
			localMessage := MsgStructToLocalChatLog(s)
			err := c.db.InsertMessage(ctx, lc.ConversationID, localMessage)
			if err != nil {
				log.ZError(ctx, "ZZWWZZWWZZ 插入消息到本地数据库失败", err,
					"clientMsgID", s.ClientMsgID,
					"conversationID", lc.ConversationID)
				return nil, err
			}
			log.ZDebug(ctx, "ZZWWZZWWZZ 消息成功插入本地数据库",
				"clientMsgID", s.ClientMsgID,
				"conversationID", lc.ConversationID)

			err = c.db.InsertSendingMessage(ctx, &model_struct.LocalSendingMessages{
				ConversationID: lc.ConversationID,
				ClientMsgID:    localMessage.ClientMsgID,
			})
			if err != nil {
				log.ZError(ctx, "ZZWWZZWWZZ 插入发送中消息记录失败", err,
					"clientMsgID", s.ClientMsgID,
					"conversationID", lc.ConversationID)
				return nil, err
			}
			log.ZDebug(ctx, "ZZWWZZWWZZ 发送中消息记录插入成功",
				"clientMsgID", s.ClientMsgID)
		} else {
			if oldMessage.Status != constant.MsgStatusSendFailed {
				log.ZDebug(ctx, "ZZWWZZWWZZ 消息已存在且状态不为发送失败，拒绝重复发送",
					"clientMsgID", s.ClientMsgID,
					"currentStatus", oldMessage.Status)
				return nil, sdkerrs.ErrMsgRepeated
			} else {
				log.ZDebug(ctx, "ZZWWZZWWZZ 消息存在但状态为发送失败，重新发送",
					"clientMsgID", s.ClientMsgID)
				s.Status = constant.MsgStatusSending
				err = c.db.InsertSendingMessage(ctx, &model_struct.LocalSendingMessages{
					ConversationID: lc.ConversationID,
					ClientMsgID:    s.ClientMsgID,
				})
				if err != nil {
					log.ZError(ctx, "ZZWWZZWWZZ 重新插入发送中消息记录失败", err,
						"clientMsgID", s.ClientMsgID)
					return nil, err
				}
			}
		}
	}

	// 更新会话最新消息
	lc.LatestMsg = utils.StructToJsonString(s)
	log.ZDebug(ctx, "ZZWWZZWWZZ 准备处理消息内容",
		"clientMsgID", s.ClientMsgID,
		"contentType", s.ContentType)

	// 处理消息内容（无需OSS上传）
	var delFile []string
	switch s.ContentType {
	case constant.Picture:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理非OSS图片消息", "clientMsgID", s.ClientMsgID)
		s.Content = utils.StructToJsonString(s.PictureElem)
	case constant.Sound:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理非OSS音频消息", "clientMsgID", s.ClientMsgID)
		s.Content = utils.StructToJsonString(s.SoundElem)
	case constant.Video:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理非OSS视频消息", "clientMsgID", s.ClientMsgID)
		s.Content = utils.StructToJsonString(s.VideoElem)
	case constant.File:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理非OSS文件消息", "clientMsgID", s.ClientMsgID)
		s.Content = utils.StructToJsonString(s.FileElem)
	case constant.Text:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理文本消息", "clientMsgID", s.ClientMsgID)
		s.Content = utils.StructToJsonString(s.TextElem)
	case constant.AtText:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理@文本消息", "clientMsgID", s.ClientMsgID)
		s.Content = utils.StructToJsonString(s.AtTextElem)
	case constant.Location:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理位置消息", "clientMsgID", s.ClientMsgID)
		s.Content = utils.StructToJsonString(s.LocationElem)
	case constant.Custom:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理自定义消息", "clientMsgID", s.ClientMsgID)
		s.Content = utils.StructToJsonString(s.CustomElem)
	case constant.Merger:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理合并消息", "clientMsgID", s.ClientMsgID)
		s.Content = utils.StructToJsonString(s.MergeElem)
	case constant.Quote:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理引用消息",
			"clientMsgID", s.ClientMsgID,
			"quoteClientMsgID", s.QuoteElem.QuoteMessage.ClientMsgID)
		s.Content = utils.StructToJsonString(s.QuoteElem)
	case constant.Card:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理名片消息", "clientMsgID", s.ClientMsgID)
		s.Content = utils.StructToJsonString(s.CardElem)
	case constant.Face:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理表情消息", "clientMsgID", s.ClientMsgID)
		s.Content = utils.StructToJsonString(s.FaceElem)
	case constant.AdvancedText:
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理高级文本消息", "clientMsgID", s.ClientMsgID)
		s.Content = utils.StructToJsonString(s.AdvancedTextElem)
	default:
		log.ZError(ctx, "ZZWWZZWWZZ 不支持的消息类型", nil,
			"clientMsgID", s.ClientMsgID,
			"contentType", s.ContentType)
		return nil, sdkerrs.ErrMsgContentTypeNotSupport
	}

	// 更新媒体消息的本地存储（如果需要）
	if utils.IsContainInt(int(s.ContentType), []int{constant.Picture, constant.Sound, constant.Video, constant.File}) {
		if isOnlineOnly {
			localMessage := MsgStructToLocalChatLog(s)
			log.ZDebug(ctx, "ZZWWZZWWZZ 更新本地媒体消息记录",
				"clientMsgID", s.ClientMsgID,
				"conversationID", lc.ConversationID)
			err = c.db.UpdateMessage(ctx, lc.ConversationID, localMessage)
			if err != nil {
				log.ZError(ctx, "ZZWWZZWWZZ 更新本地媒体消息失败", err,
					"clientMsgID", s.ClientMsgID)
				return nil, err
			}
		}
	}

	// 调用发送到服务器的方法
	log.ZDebug(ctx, "ZZWWZZWWZZ 准备将非OSS消息发送到服务器",
		"clientMsgID", s.ClientMsgID)
	return c.sendMessageToServer(ctx, s, lc, callback, delFile, p, options, isOnlineOnly)
}

func (c *Conversation) sendMessageToServer(ctx context.Context, s *sdk_struct.MsgStruct, lc *model_struct.LocalConversation, callback open_im_sdk_callback.SendMsgCallBack,
	delFiles []string, offlinePushInfo *sdkws.OfflinePushInfo, options map[string]bool, isOnlineOnly bool) (*sdk_struct.MsgStruct, error) {
	log.ZDebug(ctx, "ZZWWZZWWZZ 进入sendMessageToServer方法",
		"clientMsgID", s.ClientMsgID,
		"conversationID", lc.ConversationID)

	// 处理在线消息的特殊选项
	if isOnlineOnly {
		log.ZDebug(ctx, "ZZWWZZWWZZ 处理在线消息选项",
			"clientMsgID", s.ClientMsgID)
		utils.SetSwitchFromOptions(options, constant.IsHistory, false)
		utils.SetSwitchFromOptions(options, constant.IsPersistent, false)
		utils.SetSwitchFromOptions(options, constant.IsSenderSync, false)
		utils.SetSwitchFromOptions(options, constant.IsConversationUpdate, false)
		utils.SetSwitchFromOptions(options, constant.IsSenderConversationUpdate, false)
		utils.SetSwitchFromOptions(options, constant.IsUnreadCount, false)
		utils.SetSwitchFromOptions(options, constant.IsOfflinePush, false)
	}

	// 协议转换：本地MsgStruct -> 服务器需要的MsgData
	log.ZDebug(ctx, "ZZWWZZWWZZ 开始消息协议转换",
		"clientMsgID", s.ClientMsgID)
	var wsMsgData sdkws.MsgData
	copier.Copy(&wsMsgData, s)
	wsMsgData.AttachedInfo = utils.StructToJsonString(s.AttachedInfoElem)
	wsMsgData.Content = []byte(s.Content)
	wsMsgData.CreateTime = s.CreateTime
	wsMsgData.SendTime = 0
	wsMsgData.Options = options
	if wsMsgData.ContentType == constant.AtText {
		wsMsgData.AtUserIDList = s.AtTextElem.AtUserList
	}
	wsMsgData.OfflinePushInfo = offlinePushInfo
	s.Content = "" // 清空内容，避免重复传输
	log.ZDebug(ctx, "ZZWWZZWWZZ 消息协议转换完成",
		"clientMsgID", s.ClientMsgID,
		"serverMsgType", wsMsgData.ContentType)

	// 发送消息到服务器并等待响应
	var sendMsgResp sdkws.UserSendMsgResp
	log.ZDebug(ctx, "ZZWWZZWWZZ 开始通过长连接发送消息到服务器",
		"clientMsgID", s.ClientMsgID,
		"cmd", constant.SendMsg)
	err := c.LongConnMgr.SendReqWaitResp(ctx, &wsMsgData, constant.SendMsg, &sendMsgResp)
	if err != nil {
		log.ZWarn(ctx, "ZZWWZZWWZZ 发送消息到服务器失败", err,
			"clientMsgID", s.ClientMsgID)

		// 处理网络超时特殊情况
		if sdkerrs.ErrNetworkTimeOut.Is(err) && !isOnlineOnly {
			log.ZDebug(ctx, "ZZWWZZWWZZ 检测到网络超时，双重检查本地消息状态",
				"clientMsgID", s.ClientMsgID)
			oldMessage, _ := c.db.GetMessage(ctx, lc.ConversationID, s.ClientMsgID)
			if oldMessage.Status == constant.MsgStatusSendSuccess {
				log.ZDebug(ctx, "ZZWWZZWWZZ 本地消息状态为已发送，使用本地记录",
					"clientMsgID", s.ClientMsgID,
					"serverMsgID", oldMessage.ServerMsgID)
				sendMsgResp.SendTime = oldMessage.SendTime
				sendMsgResp.ClientMsgID = oldMessage.ClientMsgID
				sendMsgResp.ServerMsgID = oldMessage.ServerMsgID
			} else {
				log.ZError(ctx, "ZZWWZZWWZZ 网络超时且本地消息未发送成功", err,
					"clientMsgID", s.ClientMsgID)
				c.updateMsgStatusAndTriggerConversation(ctx, s.ClientMsgID, "", s.CreateTime,
					constant.MsgStatusSendFailed, s, lc, isOnlineOnly)
				return s, err
			}
		} else {
			// 其他错误情况，标记消息为发送失败
			c.updateMsgStatusAndTriggerConversation(ctx, s.ClientMsgID, "", s.CreateTime,
				constant.MsgStatusSendFailed, s, lc, isOnlineOnly)
			return s, err
		}
	}

	// 处理服务器返回的成功响应
	log.ZDebug(ctx, "ZZWWZZWWZZ 消息成功发送到服务器",
		"clientMsgID", s.ClientMsgID,
		"serverMsgID", sendMsgResp.ServerMsgID,
		"sendTime", sendMsgResp.SendTime)
	s.SendTime = sendMsgResp.SendTime
	s.Status = constant.MsgStatusSendSuccess
	s.ServerMsgID = sendMsgResp.ServerMsgID

	// 异步清理临时文件并更新消息状态
	go func() {
		log.ZDebug(ctx, "ZZWWZZWWZZ 开始异步处理消息发送后操作",
			"clientMsgID", s.ClientMsgID)

		// 删除临时文件
		for _, file := range delFiles {
			err := os.Remove(file)
			if err != nil {
				log.ZError(ctx, "ZZWWZZWWZZ 删除临时文件失败", err,
					"filePath", file,
					"clientMsgID", s.ClientMsgID)
			} else {
				log.ZDebug(ctx, "ZZWWZZWWZZ 临时文件删除成功",
					"filePath", file,
					"clientMsgID", s.ClientMsgID)
			}
		}

		// 更新消息状态并触发会话更新
		c.updateMsgStatusAndTriggerConversation(ctx, sendMsgResp.ClientMsgID, sendMsgResp.ServerMsgID, sendMsgResp.SendTime, constant.MsgStatusSendSuccess, s, lc, isOnlineOnly)
		log.ZDebug(ctx, "ZZWWZZWWZZ 消息发送后异步操作完成",
			"clientMsgID", s.ClientMsgID)
	}()

	return s, nil
}

func (c *Conversation) FindMessageList(ctx context.Context, req []*sdk_params_callback.ConversationArgs) (*sdk_params_callback.FindMessageListCallback, error) {
	var r sdk_params_callback.FindMessageListCallback
	type tempConversationAndMessageList struct {
		conversation *model_struct.LocalConversation
		msgIDList    []string
	}
	var s []*tempConversationAndMessageList
	for _, conversationsArgs := range req {
		localConversation, err := c.db.GetConversation(ctx, conversationsArgs.ConversationID)
		if err != nil {
			log.ZError(ctx, "GetConversation err", err, "conversationsArgs", conversationsArgs)
		} else {
			t := new(tempConversationAndMessageList)
			t.conversation = localConversation
			t.msgIDList = conversationsArgs.ClientMsgIDList
			s = append(s, t)
		}
	}
	for _, v := range s {
		messages, err := c.db.GetMessagesByClientMsgIDs(ctx, v.conversation.ConversationID, v.msgIDList)
		if err == nil {
			var tempMessageList []*sdk_struct.MsgStruct
			for _, message := range messages {
				temp := LocalChatLogToMsgStruct(message)
				tempMessageList = append(tempMessageList, temp)
			}
			findResultItem := sdk_params_callback.SearchByConversationResult{}
			findResultItem.ConversationID = v.conversation.ConversationID
			findResultItem.FaceURL = v.conversation.FaceURL
			findResultItem.ShowName = v.conversation.ShowName
			findResultItem.ConversationType = v.conversation.ConversationType
			findResultItem.MessageList = tempMessageList
			findResultItem.MessageCount = len(findResultItem.MessageList)
			r.FindResultItems = append(r.FindResultItems, &findResultItem)
			r.TotalCount += findResultItem.MessageCount
		} else {
			log.ZError(ctx, "GetMessagesByClientMsgIDs err", err, "conversationID", v.conversation.ConversationID, "msgIDList", v.msgIDList)
		}
	}
	return &r, nil

}

func (c *Conversation) GetAdvancedHistoryMessageList(ctx context.Context, req sdk_params_callback.GetAdvancedHistoryMessageListParams) (*sdk_params_callback.GetAdvancedHistoryMessageListCallback, error) {
	result, err := c.getAdvancedHistoryMessageList(ctx, req, false)
	if err != nil {
		return nil, err
	}
	if len(result.MessageList) == 0 {
		s := make([]*sdk_struct.MsgStruct, 0)
		result.MessageList = s
	}
	return result, nil
}

func (c *Conversation) GetAdvancedHistoryMessageListReverse(ctx context.Context, req sdk_params_callback.GetAdvancedHistoryMessageListParams) (*sdk_params_callback.GetAdvancedHistoryMessageListCallback, error) {
	result, err := c.getAdvancedHistoryMessageList(ctx, req, true)
	if err != nil {
		return nil, err
	}
	if len(result.MessageList) == 0 {
		s := make([]*sdk_struct.MsgStruct, 0)
		result.MessageList = s
	}
	return result, nil
}

func (c *Conversation) RevokeMessage(ctx context.Context, conversationID, clientMsgID string) error {
	return c.revokeOneMessage(ctx, conversationID, clientMsgID)
}

func (c *Conversation) TypingStatusUpdate(ctx context.Context, recvID, msgTip string) error {
	return c.typingStatusUpdate(ctx, recvID, msgTip)
}

func (c *Conversation) MarkConversationMessageAsRead(ctx context.Context, conversationID string) error {
	return c.markConversationMessageAsRead(ctx, conversationID)
}

func (c *Conversation) MarkAllConversationMessageAsRead(ctx context.Context) error {
	conversationIDs, err := c.db.FindAllUnreadConversationConversationID(ctx)
	if err != nil {
		return err
	}
	for _, conversationID := range conversationIDs {
		if err = c.markConversationMessageAsRead(ctx, conversationID); err != nil {
			return err
		}
	}
	return nil
}

// deprecated
func (c *Conversation) MarkMessagesAsReadByMsgID(ctx context.Context, conversationID string, clientMsgIDs []string) error {
	return c.markMessagesAsReadByMsgID(ctx, conversationID, clientMsgIDs)
}

func (c *Conversation) DeleteMessageFromLocalStorage(ctx context.Context, conversationID string, clientMsgID string) error {
	return c.deleteMessageFromLocal(ctx, conversationID, clientMsgID)
}

func (c *Conversation) DeleteMessage(ctx context.Context, conversationID string, clientMsgID string) error {
	return c.deleteMessage(ctx, conversationID, clientMsgID)
}

func (c *Conversation) DeleteAllMsgFromLocalAndServer(ctx context.Context) error {
	return c.deleteAllMsgFromLocalAndServer(ctx)
}

func (c *Conversation) DeleteAllMessageFromLocalStorage(ctx context.Context) error {
	return c.deleteAllMsgFromLocal(ctx, true)
}

func (c *Conversation) ClearConversationAndDeleteAllMsg(ctx context.Context, conversationID string) error {
	return c.clearConversationFromLocalAndServer(ctx, conversationID, c.db.ClearConversation)
}

func (c *Conversation) DeleteConversationAndDeleteAllMsg(ctx context.Context, conversationID string) error {
	return c.clearConversationFromLocalAndServer(ctx, conversationID, c.db.ResetConversation)
}

func (c *Conversation) InsertSingleMessageToLocalStorage(ctx context.Context, s *sdk_struct.MsgStruct, recvID, sendID string) (*sdk_struct.MsgStruct, error) {
	if recvID == "" || sendID == "" {
		return nil, sdkerrs.ErrArgs
	}
	var conversation model_struct.LocalConversation
	if sendID != c.loginUserID {
		faceUrl, name, err := c.getUserNameAndFaceURL(ctx, sendID)
		if err != nil {
			//log.Error(operationID, "GetUserNameAndFaceURL err", err.Error(), sendID)
		}
		s.SenderFaceURL = faceUrl
		s.SenderNickname = name
		conversation.FaceURL = faceUrl
		conversation.ShowName = name
		conversation.UserID = sendID
		conversation.ConversationID = c.getConversationIDBySessionType(sendID, constant.SingleChatType)

	} else {
		conversation.UserID = recvID
		conversation.ConversationID = c.getConversationIDBySessionType(recvID, constant.SingleChatType)
		_, err := c.db.GetConversation(ctx, conversation.ConversationID)
		if err != nil {
			faceUrl, name, err := c.getUserNameAndFaceURL(ctx, recvID)
			if err != nil {
				return nil, err
			}
			conversation.FaceURL = faceUrl
			conversation.ShowName = name
		}
	}

	s.SendID = sendID
	s.RecvID = recvID
	s.ClientMsgID = utils.GetMsgID(s.SendID)
	s.SendTime = utils.GetCurrentTimestampByMill()
	s.SessionType = constant.SingleChatType
	s.Status = constant.MsgStatusSendSuccess
	localMessage := MsgStructToLocalChatLog(s)
	conversation.LatestMsg = utils.StructToJsonString(s)
	conversation.ConversationType = constant.SingleChatType
	conversation.LatestMsgSendTime = s.SendTime
	err := c.insertMessageToLocalStorage(ctx, conversation.ConversationID, localMessage)
	if err != nil {
		return nil, err
	}
	_ = common.TriggerCmdUpdateConversation(ctx, common.UpdateConNode{ConID: conversation.ConversationID, Action: constant.AddConOrUpLatMsg, Args: conversation}, c.GetCh())
	return s, nil

}

func (c *Conversation) InsertGroupMessageToLocalStorage(ctx context.Context, s *sdk_struct.MsgStruct, groupID, sendID string) (*sdk_struct.MsgStruct, error) {
	if groupID == "" || sendID == "" {
		return nil, sdkerrs.ErrArgs
	}
	var conversation model_struct.LocalConversation
	var err error
	_, conversation.ConversationType, err = c.getConversationTypeByGroupID(ctx, groupID)
	if err != nil {
		return nil, err
	}

	conversation.ConversationID = c.getConversationIDBySessionType(groupID, int(conversation.ConversationType))
	if sendID != c.loginUserID {
		faceUrl, name, err := c.getUserNameAndFaceURL(ctx, sendID)
		if err != nil {
			// log.Error("", "getUserNameAndFaceUrlByUid err", err.Error(), sendID)
		}
		s.SenderFaceURL = faceUrl
		s.SenderNickname = name
	}
	s.SendID = sendID
	s.RecvID = groupID
	s.GroupID = groupID
	s.ClientMsgID = utils.GetMsgID(s.SendID)
	s.SendTime = utils.GetCurrentTimestampByMill()
	s.SessionType = conversation.ConversationType
	s.Status = constant.MsgStatusSendSuccess
	localMessage := MsgStructToLocalChatLog(s)
	conversation.LatestMsg = utils.StructToJsonString(s)
	conversation.LatestMsgSendTime = s.SendTime
	conversation.FaceURL = s.SenderFaceURL
	conversation.ShowName = s.SenderNickname
	err = c.insertMessageToLocalStorage(ctx, conversation.ConversationID, localMessage)
	if err != nil {
		return nil, err
	}
	_ = common.TriggerCmdUpdateConversation(ctx, common.UpdateConNode{ConID: conversation.ConversationID, Action: constant.AddConOrUpLatMsg, Args: conversation}, c.GetCh())
	return s, nil

}

func (c *Conversation) SearchLocalMessages(ctx context.Context, searchParam *sdk_params_callback.SearchLocalMessagesParams) (*sdk_params_callback.SearchLocalMessagesCallback, error) {
	searchParam.KeywordList = utils.TrimStringList(searchParam.KeywordList)
	return c.searchLocalMessages(ctx, searchParam)

}
func (c *Conversation) SetMessageLocalEx(ctx context.Context, conversationID string, clientMsgID string, localEx string) error {
	err := c.db.UpdateColumnsMessage(ctx, conversationID, clientMsgID, map[string]interface{}{"local_ex": localEx})
	if err != nil {
		return err
	}
	conversation, err := c.db.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	var latestMsg sdk_struct.MsgStruct
	utils.JsonStringToStruct(conversation.LatestMsg, &latestMsg)
	if latestMsg.ClientMsgID == clientMsgID {
		log.ZDebug(ctx, "latestMsg local ex changed", "seq", latestMsg.Seq, "clientMsgID", latestMsg.ClientMsgID)
		latestMsg.LocalEx = localEx
		latestMsgStr := utils.StructToJsonString(latestMsg)
		if err = c.db.UpdateColumnsConversation(ctx, conversationID, map[string]interface{}{"latest_msg": latestMsgStr, "latest_msg_send_time": latestMsg.SendTime}); err != nil {
			return err
		}
		c.doUpdateConversation(common.Cmd2Value{Value: common.UpdateConNode{Action: constant.ConChange, Args: []string{conversationID}}})
	}
	return nil
}

func (c *Conversation) initBasicInfo(ctx context.Context, message *sdk_struct.MsgStruct, msgFrom, contentType int32) error {
	message.CreateTime = utils.GetCurrentTimestampByMill()
	message.SendTime = message.CreateTime
	message.IsRead = false
	message.Status = constant.MsgStatusSending
	message.SendID = c.loginUserID
	userInfo, err := c.user.GetUserInfoWithCache(ctx, c.loginUserID)
	if err != nil {
		return err
	}
	message.SenderFaceURL = userInfo.FaceURL
	message.SenderNickname = userInfo.Nickname
	ClientMsgID := utils.GetMsgID(message.SendID)
	message.ClientMsgID = ClientMsgID
	message.MsgFrom = msgFrom
	message.ContentType = contentType
	message.SenderPlatformID = c.platformID
	return nil
}

func (c *Conversation) getConversationTypeByGroupID(ctx context.Context, groupID string) (conversationID string, conversationType int32, err error) {
	g, err := c.group.FetchGroupOrError(ctx, groupID)
	if err != nil {
		return "", 0, errs.WrapMsg(err, "get group info error")
	}
	switch g.GroupType {
	case constant.NormalGroup:
		return c.getConversationIDBySessionType(groupID, constant.WriteGroupChatType), constant.WriteGroupChatType, nil
	case constant.SuperGroup, constant.WorkingGroup:
		return c.getConversationIDBySessionType(groupID, constant.ReadGroupChatType), constant.ReadGroupChatType, nil
	default:
		return "", 0, sdkerrs.ErrGroupType
	}
}

func (c *Conversation) SearchConversation(ctx context.Context, searchParam string) ([]*server_api_params.Conversation, error) {
	// Check if search parameter is empty
	if searchParam == "" {
		return nil, sdkerrs.ErrArgs.WrapMsg("search parameter cannot be empty")
	}

	// Perform the search in your database or data source
	// This is a placeholder for the actual database call
	conversations, err := c.db.SearchConversations(ctx, searchParam)
	if err != nil {
		// Handle any errors that occurred during the search
		return nil, err
	}
	apiConversations := make([]*server_api_params.Conversation, len(conversations))
	for i, localConv := range conversations {
		// Create new server_api_params.Conversation and map fields from localConv
		apiConv := &server_api_params.Conversation{
			ConversationID:        localConv.ConversationID,
			ConversationType:      localConv.ConversationType,
			UserID:                localConv.UserID,
			GroupID:               localConv.GroupID,
			RecvMsgOpt:            localConv.RecvMsgOpt,
			UnreadCount:           localConv.UnreadCount,
			DraftTextTime:         localConv.DraftTextTime,
			IsPinned:              localConv.IsPinned,
			IsPrivateChat:         localConv.IsPrivateChat,
			BurnDuration:          localConv.BurnDuration,
			GroupAtType:           localConv.GroupAtType,
			IsNotInGroup:          localConv.IsNotInGroup,
			UpdateUnreadCountTime: localConv.UpdateUnreadCountTime,
			AttachedInfo:          localConv.AttachedInfo,
			Ex:                    localConv.Ex,
		}
		apiConversations[i] = apiConv
	}
	// Return the list of conversations
	return apiConversations, nil
}
func (c *Conversation) GetInputStates(ctx context.Context, conversationID string, userID string) ([]int32, error) {
	return c.typing.GetInputStates(conversationID, userID), nil
}

func (c *Conversation) ChangeInputStates(ctx context.Context, conversationID string, focus bool) error {
	return c.typing.ChangeInputStates(ctx, conversationID, focus)
}
