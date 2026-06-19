package realtime

import (
	"fmt"
	"sync"
	"time"
)

// Manager is the SSE publisher: the subscriber registry and the broadcast
// fan-out. It owns its own locks (independent of the server's write mutex) so
// DB write transactions never block event fan-out, and every send is
// non-blocking (buffered channel + drop-oldest) so a slow or dead subscriber
// can never stall the broadcaster or an editor.
//
// The server holds one Manager and drives it: it assigns per-scope sequence
// numbers, wraps payloads as envelopes (see EventSnapshotJSON/EventDeltaJSON)
// and coalesces deltas, then calls Broadcast/BroadcastTo to fan out. The
// Manager itself is scope-agnostic — it just routes Events to channels.
type Manager struct {
	// subMu guards both subscriber maps independently of the server's write
	// mutex so DB writes don't block event fan-out.
	subMu sync.RWMutex
	// subscribers maps fest -> SSE channel -> SubInfo. Editors (fest organizers)
	// get every state delta immediately; viewers get coalesced merged deltas.
	subscribers     map[int64]map[chan Event]SubInfo
	hostSubscribers map[int64]map[chan HostPresenceEvent]struct{}

	// viewerCount* throttle the "viewers" tally fan-out so a connection storm
	// costs at most one fan-out per fest per viewerCountInterval.
	viewerCountMu    sync.Mutex
	viewerCountAt    map[int64]time.Time
	viewerCountTimer map[int64]*time.Timer
}

// NewManager returns a Manager with its maps initialised.
func NewManager() *Manager {
	return &Manager{
		subscribers:      make(map[int64]map[chan Event]SubInfo),
		hostSubscribers:  make(map[int64]map[chan HostPresenceEvent]struct{}),
		viewerCountAt:    make(map[int64]time.Time),
		viewerCountTimer: make(map[int64]*time.Timer),
	}
}

// Event is one item queued on a subscriber channel. Name is the SSE event name;
// empty means "state" (the common case); "viewers" carries the concurrent
// tally, "lockdown" is a server sentinel telling a viewer to drop the stream.
type Event struct {
	FestID   int64
	Revision int64
	Data     []byte
	Name     string
}

// HostPresenceEvent is one item queued on a host-presence (/host-events) channel.
type HostPresenceEvent struct {
	FestID int64
	Data   []byte
}

// SubInfo records per SSE connection whether it is an editor (organizer) vs a
// spectator, and which game it is watching (0 when unscoped). GameID partitions
// the concurrent-viewer tally so each game reports only its own spectators.
type SubInfo struct {
	Editor bool
	GameID int64
}

// Audience selects which SSE subscribers a broadcast reaches.
type Audience int

const (
	AudAll     Audience = iota // editors + viewers (snapshots, fest-wide events)
	AudEditors                 // organizers only — immediate, uncoalesced deltas
	AudViewers                 // spectators only — coalesced merged deltas
)

// viewerCountInterval bounds how often the concurrent-viewer tally is fanned
// out. A tally a few seconds stale is fine; freeing the CPU during connection
// churn is not.
const viewerCountInterval = 10 * time.Second

// AddSubscriber registers an SSE connection for a fest.
func (m *Manager) AddSubscriber(festID int64, ch chan Event, isEditor bool, gameID int64) {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	if m.subscribers == nil {
		m.subscribers = make(map[int64]map[chan Event]SubInfo)
	}
	bucket, ok := m.subscribers[festID]
	if !ok {
		bucket = make(map[chan Event]SubInfo)
		m.subscribers[festID] = bucket
	}
	bucket[ch] = SubInfo{Editor: isEditor, GameID: gameID}
}

// RemoveSubscriber deregisters an SSE connection. It deliberately does NOT
// close(ch): BroadcastTo snapshots the channel list under subMu.RLock and sends
// AFTER releasing the lock, so closing here would race that send and panic
// ("send on closed channel") — fatal, since broadcasts also run from the
// detached delta-coalescing timer goroutine with no net/http recover above it.
// Removal from the map is enough; the reader exits on ctx.Done()/lockdown and
// never relies on close as a signal.
func (m *Manager) RemoveSubscriber(festID int64, ch chan Event) {
	m.subMu.Lock()
	if bucket, ok := m.subscribers[festID]; ok {
		delete(bucket, ch)
		if len(bucket) == 0 {
			delete(m.subscribers, festID)
		}
	}
	m.subMu.Unlock()
}

// Broadcast fans an event to all subscribers (editors + viewers).
func (m *Manager) Broadcast(ev Event) { m.BroadcastTo(ev, AudAll) }

// BroadcastTo fans an event to the selected audience. It snapshots the channel
// list under the read lock, then sends without the lock held; every send is
// non-blocking with drop-oldest so a slow subscriber never stalls the fan-out.
func (m *Manager) BroadcastTo(ev Event, aud Audience) {
	var chs []chan Event
	m.subMu.RLock()
	if bucket, ok := m.subscribers[ev.FestID]; ok && len(bucket) > 0 {
		chs = make([]chan Event, 0, len(bucket))
		for ch, info := range bucket {
			switch aud {
			case AudEditors:
				if !info.Editor {
					continue
				}
			case AudViewers:
				if info.Editor {
					continue
				}
			}
			chs = append(chs, ch)
		}
	}
	m.subMu.RUnlock()
	for _, ch := range chs {
		sendDropOldest(ch, ev)
	}
}

// BroadcastLockdown sends the "lockdown" sentinel to viewers (not editors) so
// they drop the stream and reload into the now-static page.
func (m *Manager) BroadcastLockdown() {
	m.subMu.RLock()
	defer m.subMu.RUnlock()
	ev := Event{Name: "lockdown"}
	for _, bucket := range m.subscribers {
		for ch, info := range bucket {
			if info.Editor {
				continue
			}
			sendDropOldest(ch, ev)
		}
	}
}

// ScheduleViewerCount fans out the viewer tally at most once per fest per
// viewerCountInterval. The first change after a quiet period broadcasts
// immediately (leading edge); changes during the cooldown collapse into a
// single trailing broadcast at the end of the window. Eventual consistency is
// guaranteed: the trailing timer always sends the final count.
func (m *Manager) ScheduleViewerCount(festID int64) {
	m.viewerCountMu.Lock()
	defer m.viewerCountMu.Unlock()
	if m.viewerCountAt == nil {
		m.viewerCountAt = make(map[int64]time.Time)
		m.viewerCountTimer = make(map[int64]*time.Timer)
	}
	now := time.Now()
	if since := now.Sub(m.viewerCountAt[festID]); since >= viewerCountInterval {
		m.viewerCountAt[festID] = now
		go m.BroadcastViewerCount(festID) // off-lock: fan-out must not hold viewerCountMu
		return
	}
	if m.viewerCountTimer[festID] != nil {
		return // a trailing broadcast is already pending
	}
	delay := viewerCountInterval - now.Sub(m.viewerCountAt[festID])
	m.viewerCountTimer[festID] = time.AfterFunc(delay, func() {
		m.viewerCountMu.Lock()
		m.viewerCountAt[festID] = time.Now()
		m.viewerCountTimer[festID] = nil
		m.viewerCountMu.Unlock()
		m.BroadcastViewerCount(festID)
	})
}

// BroadcastViewerCount fans out the current spectator count for a fest as a
// "viewers" SSE event, partitioned per game. Unlike BroadcastTo it holds
// subMu.RLock for the whole fan-out: viewer-count events fire exactly when
// subscribers churn, so the lock guarantees no subscriber is removed mid-send.
func (m *Manager) BroadcastViewerCount(festID int64) {
	m.subMu.RLock()
	defer m.subMu.RUnlock()
	bucket := m.subscribers[festID]
	// Tally spectators PER GAME (editors are participants, not spectators, so they
	// are excluded from the count — but they still receive the event so a host can
	// see how many people are watching their game).
	counts := make(map[int64]int, len(bucket))
	for _, info := range bucket {
		if info.Editor {
			continue
		}
		counts[info.GameID]++
	}
	// Cache one payload per distinct game so a fest-wide fan-out marshals each
	// count once, not once per channel.
	payloads := make(map[int64][]byte, len(counts))
	for ch, info := range bucket {
		data, ok := payloads[info.GameID]
		if !ok {
			data = []byte(fmt.Sprintf(`{"count":%d}`, counts[info.GameID]))
			payloads[info.GameID] = data
		}
		sendDropOldest(ch, Event{FestID: festID, Name: "viewers", Data: data})
	}
}

// AddHostSubscriber registers a host-presence (/host-events) connection.
func (m *Manager) AddHostSubscriber(festID int64, ch chan HostPresenceEvent) {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	if m.hostSubscribers == nil {
		m.hostSubscribers = make(map[int64]map[chan HostPresenceEvent]struct{})
	}
	bucket, ok := m.hostSubscribers[festID]
	if !ok {
		bucket = make(map[chan HostPresenceEvent]struct{})
		m.hostSubscribers[festID] = bucket
	}
	bucket[ch] = struct{}{}
}

// RemoveHostSubscriber deregisters a host-presence connection and closes ch.
// Closing is safe here (unlike RemoveSubscriber): host-presence is never sent
// from a detached timer, so there is no concurrent post-lock send to race.
func (m *Manager) RemoveHostSubscriber(festID int64, ch chan HostPresenceEvent) {
	m.subMu.Lock()
	if bucket, ok := m.hostSubscribers[festID]; ok {
		delete(bucket, ch)
		if len(bucket) == 0 {
			delete(m.hostSubscribers, festID)
		}
	}
	m.subMu.Unlock()
	close(ch)
}

// BroadcastHostPresence fans a host-presence event to all host-presence
// subscribers for the fest.
func (m *Manager) BroadcastHostPresence(ev HostPresenceEvent) {
	var chs []chan HostPresenceEvent
	m.subMu.RLock()
	if bucket, ok := m.hostSubscribers[ev.FestID]; ok && len(bucket) > 0 {
		chs = make([]chan HostPresenceEvent, 0, len(bucket))
		for ch := range bucket {
			chs = append(chs, ch)
		}
	}
	m.subMu.RUnlock()
	for _, ch := range chs {
		sendHostDropOldest(ch, ev)
	}
}

// sendDropOldest does a non-blocking send; if the buffer is full it drops the
// oldest queued event and retries, so a late subscriber always sees the latest.
func sendDropOldest(ch chan Event, ev Event) {
	select {
	case ch <- ev:
	default:
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- ev:
		default:
		}
	}
}

func sendHostDropOldest(ch chan HostPresenceEvent, ev HostPresenceEvent) {
	select {
	case ch <- ev:
	default:
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- ev:
		default:
		}
	}
}
