package controllers

import (
	"github.com/antoniodipinto/ikisocket"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type websocketController struct {
	kws            *ikisocket.Websocket
	token          string
	participant    config.ChatParticipant
	authTokenModel *models.AuthTokenModel
}

func newWebsocketController(kws *ikisocket.Websocket) *websocketController {
	authToken := kws.Query("token")
	roomSid := kws.Query("roomSid")
	userSid := kws.Query("userSid")
	roomId := kws.Query("roomId")
	userId := kws.Query("userId")

	p := config.ChatParticipant{
		RoomSid: roomSid,
		RoomId:  roomId,
		UserSid: userSid,
		UserId:  userId,
		UUID:    kws.UUID,
	}

	return &websocketController{
		kws:            kws,
		participant:    p,
		token:          authToken,
		authTokenModel: models.NewAuthTokenModel(),
	}
}

func (c *websocketController) validation() bool {
	if c.token == "" {
		_ = c.kws.EmitTo(c.kws.UUID, []byte("empty auth token"), ikisocket.TextMessage)
		return false
	}

	claims, err := c.authTokenModel.VerifyPlugNmeetAccessToken(c.token)
	if err != nil {
		_ = c.kws.EmitTo(c.kws.UUID, []byte("invalid auth token"), ikisocket.TextMessage)
		return false
	}

	if claims.UserId != c.participant.UserId || claims.RoomId != c.participant.RoomId {
		_ = c.kws.EmitTo(c.kws.UUID, []byte("unauthorized access!"), ikisocket.TextMessage)
		return false
	}

	c.participant.Name = claims.Name
	// default set false
	c.kws.SetAttribute("isAdmin", claims.IsAdmin)
	c.participant.IsAdmin = claims.IsAdmin

	return true
}

func (c *websocketController) addUser() {
	config.AppCnf.AddChatUser(c.participant.RoomId, c.participant)
	c.kws.SetAttribute("userId", c.participant.UserId)
	c.kws.SetAttribute("roomId", c.participant.RoomId)
}

func HandleWebSocket() func(*fiber.Ctx) error {
	return ikisocket.New(func(kws *ikisocket.Websocket) {
		wc := newWebsocketController(kws)
		isValid := wc.validation()

		if isValid {
			wc.addUser()
		} else {
			if kws.IsAlive() {
				kws.Close()
			}
		}
	})
}

func SetupSocketListeners() {
	analytics := models.NewAnalyticsModel()
	// On message event
	ikisocket.On(ikisocket.EventMessage, func(ep *ikisocket.EventPayload) {
		//fmt.Println(fmt.Sprintf("Message event - User: %s - Message: %s", ep.Kws.GetStringAttribute("userId"), string(ep.Data)))
		dataMsg := &plugnmeet.DataMessage{}
		err := proto.Unmarshal(ep.Data, dataMsg)
		if err != nil {
			return
		}

		// for analytics type data we won't need to deliver anywhere
		if dataMsg.Body.Type == plugnmeet.DataMsgBodyType_ANALYTICS_DATA {
			ad := new(plugnmeet.AnalyticsDataMsg)
			err = protojson.Unmarshal([]byte(dataMsg.Body.Msg), ad)
			if err != nil {
				return
			}
			analytics.HandleEvent(ad)
			return
		}

		roomId := ep.Kws.GetStringAttribute("roomId")
		payload := &models.WebsocketToRedis{
			Type:    "sendMsg",
			DataMsg: dataMsg,
			RoomId:  roomId,
			IsAdmin: false,
		}
		isAdmin := ep.Kws.GetAttribute("isAdmin")
		if isAdmin != nil {
			payload.IsAdmin = isAdmin.(bool)
		}

		models.DistributeWebsocketMsgToRedisChannel(payload)
		// send analytics
		if dataMsg.Body.From != nil && dataMsg.Body.From.UserId != "" {
			analytics.HandleWebSocketData(dataMsg)
		}

	})

	// On disconnect event
	ikisocket.On(ikisocket.EventDisconnect, func(ep *ikisocket.EventPayload) {
		roomId := ep.Kws.GetStringAttribute("roomId")
		userId := ep.Kws.GetStringAttribute("userId")
		// Remove the user from the local clients
		config.AppCnf.RemoveChatParticipant(roomId, userId)
	})

	// This event is called when the server disconnects the user actively with .Close() method
	ikisocket.On(ikisocket.EventClose, func(ep *ikisocket.EventPayload) {
		roomId := ep.Kws.GetStringAttribute("roomId")
		userId := ep.Kws.GetStringAttribute("userId")
		// Remove the user from the local clients
		config.AppCnf.RemoveChatParticipant(roomId, userId)
	})

	// On error event
	//ikisocket.On(ikisocket.EventError, func(ep *ikisocket.EventPayload) {
	//	log.Errorln(fmt.Sprintf("Error event - User: %s", ep.Kws.GetStringAttribute("userSid")), ep.Error)
	//})
}

// SubscribeToWebsocketChannel will subscribe to all websocket channels
func SubscribeToWebsocketChannel() {
	go models.SubscribeToUserWebsocketChannel()
	go models.SubscribeToWhiteboardWebsocketChannel()
	go models.SubscribeToSystemWebsocketChannel()
}
