package webrtc

import (
	"encoding/json"
	"log"
	"os"
	"sync"

	"github.com/gofiber/websocket/v2"
	"github.com/pion/webrtc/v3"
)

func StreamConn(c *websocket.Conn, p *Peers) {
	// get the webrtc configuration
	var config webrtc.Configuration
	if os.Getenv("ENVIRONMENT") == "PRODUCTION" {
		config = turnConfig
	}

	// create a new peer connection for this stream
	peerConnection, err := webrtc.NewPeerConnection(config)
	if err != nil {
		log.Print(err)
		return
	}
	// close the peer connection when the function returns
	defer func() {
		if cErr := peerConnection.Close(); cErr != nil {
			log.Print(cErr)
		}
	}()

	// setup the receiving RTP streams for audio and video data types
	for _, typ := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeVideo, webrtc.RTPCodecTypeAudio} {
		if _, err := peerConnection.AddTransceiverFromKind(typ, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		}); err != nil {
			log.Print(err)
			return
		}
	}

	// create a new peer connection state for this stream
	newPeer := PeerConnectionState{
		PeerConnection: peerConnection,
		Websocket: &ThreadSafeWriter{
			Conn:  c,
			Mutex: sync.Mutex{},
		}}

	// Add our new PeerConnection to global list
	p.ListLock.Lock()
	p.Connections = append(p.Connections, newPeer)
	p.ListLock.Unlock()

	// log the current list of PeerConnections
	log.Println(p.Connections)

	// Setup ICE candidate handler. Emit server candidate to client
	peerConnection.OnICECandidate(func(i *webrtc.ICECandidate) {
		// handles the case where the ICE candidate is nil (meaning that the ICE gathering process is complete)
		if i == nil {
			return
		}

		// parse the candidate details from the ICE candidate
		candidateString, err := json.Marshal(i.ToJSON())
		if err != nil {
			log.Println(err)
			return
		}

		// connect the candidate details to the websocket of this stream peer connection
		if writeErr := newPeer.Websocket.WriteJSON(&websocketMessage{
			Event: "candidate",
			Data:  string(candidateString),
		}); writeErr != nil {
			log.Println(writeErr)
			return
		}
	})

	// Setup hanlder for connection state change
	peerConnection.OnConnectionStateChange(func(pp webrtc.PeerConnectionState) {
		switch pp {
		case webrtc.PeerConnectionStateFailed:
			if err := peerConnection.Close(); err != nil {
				log.Println(err)
			}
		case webrtc.PeerConnectionStateClosed:
			p.SignalPeerConnections()
		}
	})

	// update the list of PeerConnections
	p.SignalPeerConnections()
	// handle the websocket connection message events
	message := &websocketMessage{}
	for {
		_, raw, err := c.ReadMessage()
		if err != nil {
			log.Println(err)
			return
		} else if err := json.Unmarshal(raw, message); err != nil {
			log.Println(err)
			return
		}

		// handle each message event case
		switch message.Event {
		// if we are given a new ICE candidate then add it to the PeerConnection
		case "candidate":
			candidate := webrtc.ICECandidateInit{}
			if err := json.Unmarshal([]byte(message.Data), &candidate); err != nil {
				log.Println(err)
				return
			}

			if err := peerConnection.AddICECandidate(candidate); err != nil {
				log.Println(err)
				return
			}

		// if we are given a new answer message then set the remote description of our current connection
		case "answer":
			answer := webrtc.SessionDescription{}
			// unmarshal the answer message
			if err := json.Unmarshal([]byte(message.Data), &answer); err != nil {
				log.Println(err)
				return
			}

			// set the remote description of the current PeerConnection
			if err := peerConnection.SetRemoteDescription(answer); err != nil {
				log.Println(err)
				return
			}
		}
	}
}
