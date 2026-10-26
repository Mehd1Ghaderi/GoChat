package voiceserver

import (
	"sync"
	"testing"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// camera plays a browser's camera and screen: on its first offer it puts
// both on the slots, which it answers sendonly, and keeps sending them,
// whether the room forwards them or not, as a browser does. It counts the
// requests for a keyframe each gets.
type camera struct {
	t    *testing.T
	pc   *webrtc.PeerConnection
	mu   sync.Mutex
	mids map[string]string
	plis map[string]int
}

func (cam *camera) attach(c *client, sl *slots) {
	cam.mu.Lock()
	defer cam.mu.Unlock()
	if cam.mids != nil {
		return
	}
	cam.pc, cam.mids, cam.plis = c.pc, map[string]string{}, map[string]int{}
	for source, mid := range map[string]string{sourceCamera: sl.Camera, sourceScreen: sl.Screen} {
		track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000}, source, "local")
		if err != nil {
			cam.t.Error(err)
			return
		}
		// Pion made the slot sendonly when it took the offer, as the
		// recvonly side's peer; the track keeps it so.
		var slot *webrtc.RTPTransceiver
		for _, tr := range cam.pc.GetTransceivers() {
			if tr.Mid() == mid {
				slot = tr
			}
		}
		if slot == nil || slot.Direction() != webrtc.RTPTransceiverDirectionSendonly {
			cam.t.Errorf("no sendonly slot for the %s at mid %q", source, mid)
			return
		}
		s, err := c.api.NewRTPSender(track, cam.pc.SCTP().Transport())
		if err != nil {
			cam.t.Error(err)
			return
		}
		if err := slot.SetSender(s, track); err != nil {
			cam.t.Error(err)
			return
		}
		cam.mids[source] = mid
		go func() {
			for cam.pc.ConnectionState() != webrtc.PeerConnectionStateClosed {
				if err := track.WriteSample(media.Sample{Data: make([]byte, 600), Duration: 33 * time.Millisecond}); err != nil {
					return
				}
				time.Sleep(33 * time.Millisecond)
			}
		}()
		go func() {
			for {
				pkts, _, err := s.ReadRTCP()
				if err != nil {
					return
				}
				for _, pkt := range pkts {
					if _, ok := pkt.(*rtcp.PictureLossIndication); ok {
						cam.mu.Lock()
						cam.plis[source]++
						cam.mu.Unlock()
					}
				}
			}
		}()
	}
}

// sendsOnSlots names a slot an offer sends something on: the server only
// ever receives on them.
func sendsOnSlots(offer string, sl *slots) string {
	var d sdp.SessionDescription
	if err := d.UnmarshalString(offer); err != nil {
		return err.Error()
	}
	for _, m := range d.MediaDescriptions {
		mid, _ := m.Attribute("mid")
		if mid != sl.Camera && mid != sl.Screen {
			continue
		}
		if _, ok := m.Attribute("recvonly"); !ok {
			return "mid " + mid
		}
	}
	return ""
}

func (cam *camera) pli(source string) int {
	cam.mu.Lock()
	defer cam.mu.Unlock()
	return cam.plis[source]
}

// joinCamera joins a client that sends and shows video, with a camera.
func (s *sfu) joinCamera(session, userID string) (*client, *camera) {
	cam := &camera{t: s.t}
	c, err := s.joinWith(session, userID, true, true, cam.attach)
	if err != nil {
		s.t.Fatal(err)
	}
	return c, cam
}

// stops reports whether a stream stopped coming: packets already on their
// way may still land, however slowly a loaded machine delivers them.
func (c *client) stops(streamID string) bool {
	before := c.heard(streamID)
	time.Sleep(250 * time.Millisecond)
	return c.heard(streamID) == before
}

func (s *sfu) video(session, source string, on bool) {
	s.engine.Send(s.rooms, toRoom{channelID: "room", msg: video{session: session, source: source, on: on}})
}

func TestRoomForwardsCamerasAndScreens(t *testing.T) {
	s := newSFU(t, 3)
	alice, aliceCam := s.joinCamera("s-alice", "alice")
	bob, _ := s.joinCamera("s-bob", "bob")
	// carol's app does not do video: she hears the others and sees nothing.
	carol, err := s.join("s-carol", "carol", true)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "everyone hears alice", func() bool { return bob.heard("alice") > 10 && carol.heard("alice") > 10 })

	// Every offer to one who does video names its slots; to carol, none.
	bob.mu.Lock()
	if len(bob.slots) == 0 || bob.slots[0] == nil || bob.slots[0].Camera == "" || bob.slots[0].Screen == "" {
		t.Errorf("bob's first offer named no slots: %v", bob.slots)
	}
	bob.mu.Unlock()
	carol.mu.Lock()
	for _, sl := range carol.slots {
		if sl != nil {
			t.Errorf("carol was offered slots: %+v", *sl)
		}
	}
	carol.mu.Unlock()

	// Alice's camera comes in from the start, but nobody gets it until she
	// says she sends it.
	time.Sleep(500 * time.Millisecond)
	if bob.heard("alice:camera") != 0 {
		t.Fatal("bob got alice's camera before she turned it on")
	}

	s.video("s-alice", sourceCamera, true)
	eventually(t, "bob sees alice's camera", func() bool { return bob.heard("alice:camera") > 10 })
	eventually(t, "alice is asked for a keyframe for bob", func() bool { return aliceCam.pli(sourceCamera) > 0 })
	if carol.heard("alice:camera") != 0 || alice.heard("alice:camera") != 0 {
		t.Fatal("alice's camera went to carol, who does no video, or back to alice")
	}

	// Bob's browser asking for a keyframe reaches alice.
	plis := aliceCam.pli(sourceCamera)
	time.Sleep(keyframeGap)
	bob.mu.Lock()
	remote := bob.remotes["alice:camera"]
	bob.mu.Unlock()
	if err := bob.pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(remote.SSRC())}}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "bob's request reaches alice", func() bool { return aliceCam.pli(sourceCamera) > plis })

	// Her screen goes out beside it.
	s.video("s-alice", sourceScreen, true)
	eventually(t, "bob sees alice's screen", func() bool { return bob.heard("alice:screen") > 10 })

	// Turning the camera off stops it for bob and leaves the screen.
	s.video("s-alice", sourceCamera, false)
	eventually(t, "bob stops getting alice's camera", func() bool { return bob.stops("alice:camera") })
	screen := bob.heard("alice:screen")
	eventually(t, "the screen goes on", func() bool { return bob.heard("alice:screen") > screen+10 })

	// And on again.
	camera := bob.heard("alice:camera")
	s.video("s-alice", sourceCamera, true)
	eventually(t, "bob sees alice's camera again", func() bool { return bob.heard("alice:camera") > camera+10 })

	// A video message from one without slots changes nothing.
	s.video("s-carol", sourceCamera, true)

	// Nothing ever went to bob on his own slots.
	bob.mu.Lock()
	if len(bob.slotSends) > 0 {
		t.Errorf("offers sent to bob on his slots: %v", bob.slotSends)
	}
	bob.mu.Unlock()

	// When alice leaves, both go.
	s.engine.Send(s.rooms, disconnect{channelID: "room", session: "s-alice", reason: "left"})
	eventually(t, "bob stops getting alice's camera and screen", func() bool {
		return bob.stops("alice:camera") && bob.stops("alice:screen")
	})
	if bob.heard("carol") == 0 {
		t.Fatal("bob lost carol")
	}
}
