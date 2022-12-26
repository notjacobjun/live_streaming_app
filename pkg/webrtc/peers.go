package webrtc

import (
	"encoding/json"
	"log"
	"sync"
	"time"
	"videochat/pkg/chat"

	"github.com/gofiber/websocket/v2"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
)

var (
	RoomsLock sync.RWMutex
	Rooms     map[string]*Room
	Streams   map[string]*Room
)

// TODO figure out how to setup this config
var (
	turnConfig = webrtc.Configuration{
		ICETransportPolicy: webrtc.ICETransportPolicyRelay,
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
			{
				URLs: []string{
					"turn:relay.metered.ca:80",
				},
				Username:       "f656bb327ada11408d2cd592",
				Credential:     "D5FTwyiln3XE0vFq",
				CredentialType: webrtc.ICECredentialTypePassword,
			},
		},
	}
)

type Room struct {
	Peers *Peers
	Hub   *chat.Hub
}

type Stream struct {
	Track *webrtc.TrackLocalStaticRTP
}

type Peers struct {
	ListLock    sync.Mutex
	Connections []PeerConnectionState
	TrackLocals map[string]*webrtc.TrackLocalStaticRTP
}

type PeerConnectionState struct {
	PeerConnection *webrtc.PeerConnection
	Websocket      *ThreadSafeWriter
}

type ThreadSafeWriter struct {
	Conn  *websocket.Conn
	Mutex sync.Mutex
}

type websocketMessage struct {
	Event string `json:"event"`
	Data  string `json:"data"`
}

func (t *ThreadSafeWriter) WriteJSON(v interface{}) error {
	t.Mutex.Lock()
	defer t.Mutex.Unlock()
	return t.Conn.WriteJSON(v)
}

func (p *Peers) AddTrack(t *webrtc.TrackRemote) *webrtc.TrackLocalStaticRTP {
	// lock the list of tracks for this peer
	p.ListLock.Lock()
	defer func() {
		p.ListLock.Unlock()
		p.SignalPeerConnections()
	}()

	// create a new track local (track used to send packets to another peer) and add it to the list of tracks
	trackLocal, err := webrtc.NewTrackLocalStaticRTP(t.Codec().RTPCodecCapability, t.ID(), t.StreamID())
	if err != nil {
		log.Println(err.Error())
		return nil
	}
	p.TrackLocals[t.ID()] = trackLocal
	return trackLocal
}

func (p *Peers) RemoveTrack(t *webrtc.TrackLocalStaticRTP) {
	// lock the list of tracks for this peer
	p.ListLock.Lock()
	defer func() {
		p.ListLock.Unlock()
		p.SignalPeerConnections()
	}()

	// remove the track from the list of tracks
	delete(p.TrackLocals, t.ID())
}

func (p *Peers) SignalPeerConnections() {
	// lock the list of tracks for this peer
	p.ListLock.Lock()
	defer func() {
		p.ListLock.Unlock()
		p.DispatchKeyFrame()
	}()

	// try to sync all the peer connections
	attemptSync := func() (tryAgain bool) {
		for i := range p.Connections {
			// check if this connection was closed
			if p.Connections[i].PeerConnection.ConnectionState() == webrtc.PeerConnectionStateClosed {
				// remove the connection from the connections list
				p.Connections = append(p.Connections[:i], p.Connections[i+1:]...)
				return true
			}

			existingSenders := map[string]bool{}
			// parse all the sender tracks for each peer connection
			for _, sender := range p.Connections[i].PeerConnection.GetSenders() {
				if sender.Track() == nil {
					continue
				}
				// add this sender track to the list of existing senders
				existingSenders[sender.Track().ID()] = true

				if _, ok := p.TrackLocals[sender.Track().ID()]; !ok {
					// remove the sender track from the peer connection (only if it doesn't exist in the list of tracks)
					if err := p.Connections[i].PeerConnection.RemoveTrack(sender); err != nil {
						return true
					}
				}
			}

			// parse all the reciever tracks for each peer connection
			for _, reciever := range p.Connections[i].PeerConnection.GetReceivers() {
				if reciever.Track() == nil {
					continue
				}

				// add each reciever track to the existing senders list
				existingSenders[reciever.Track().ID()] = true
			}

			// parse all the tracks for this peer connection
			for trackID := range p.TrackLocals {
				// try to add this track local to the list of existing senders if it's not already there
				if _, ok := existingSenders[trackID]; !ok {
					if _, err := p.Connections[i].PeerConnection.AddTrack(p.TrackLocals[trackID]); err != nil {
						return true
					}
				}
			}

			// create an offer for this current peer connection
			offer, err := p.Connections[i].PeerConnection.CreateOffer(nil)
			if err != nil {
				return true
			}

			// try to set the local description for each peer connection
			if err := p.Connections[i].PeerConnection.SetLocalDescription(offer); err != nil {
				return true
			}

			// encode the offer information for each peer connection
			offerString, err := json.Marshal(offer)
			if err != nil {
				return true
			}

			// send the offer information to the peer connection's websocket
			if err := p.Connections[i].Websocket.WriteJSON(websocketMessage{
				Event: "offer",
				Data:  string(offerString),
			}); err != nil {
				return true
			}
		}
		return
	}

	// try to sync all the peer connections
	for syncAttempt := 0; ; syncAttempt++ {
		if syncAttempt == 25 {
			go func() {
				time.Sleep(3 * time.Second)
				p.SignalPeerConnections()
			}()
			return
		}

		// if the sync attempt was successful, return
		if !attemptSync() {
			return
		}
	}
}

func (p *Peers) DispatchKeyFrame() {
	// lock the list of tracks for this peer
	p.ListLock.Lock()
	defer p.ListLock.Unlock()

	// write a key frame to each peer connection's reciever tracks
	for i := range p.Connections {
		for _, receiver := range p.Connections[i].PeerConnection.GetReceivers() {
			if receiver.Track() == nil {
				continue
			}

			// writing each RTCP packet to the peer connection's reciever tracks
			_ = p.Connections[i].PeerConnection.WriteRTCP([]rtcp.Packet{
				&rtcp.PictureLossIndication{
					MediaSSRC: uint32(receiver.Track().SSRC()),
				},
			})
		}
	}
}
