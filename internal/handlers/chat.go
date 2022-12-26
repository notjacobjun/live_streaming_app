package handlers

import (
	"videochat/pkg/chat"
	w "videochat/pkg/webrtc"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
)

func RoomChat(c *fiber.Ctx) error {
	// render the html template for room chat
	return c.Render("chat", fiber.Map{}, "layouts/main")
}

func RoomChatWebsocket(c *websocket.Conn) {
	uuid := c.Params("uuid")
	if uuid == "" {
		c.Close()
		return
	}

	w.RoomsLock.Lock()
	// get the room from the global map
	room := w.Rooms[uuid]
	w.RoomsLock.Unlock()
	if room == nil {
		return
	}

	if room.Hub == nil {
		return
	}

	// add the connection to the chat hub
	chat.PeerChatConn(c.Conn, room.Hub)
}

func StreamChatWebsocket(c *websocket.Conn) {
	suuid := c.Params("suuid")
	if suuid == "" {
		return
	}

	// get the stream from the global map
	w.RoomsLock.Lock()
	if stream, ok := w.Streams[suuid]; ok {
		w.RoomsLock.Unlock()
		if stream.Hub == nil {
			// create a new chat hub
			stream.Hub = chat.NewHub()
			go stream.Hub.Run()
		}

		// add the connection to the chat hub
		chat.PeerChatConn(c.Conn, stream.Hub)
		return

	}
	w.RoomsLock.Unlock()
}
