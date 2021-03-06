package memory

import (
	"github.com/leighmacdonald/mika/consts"
	"github.com/leighmacdonald/mika/model"
	"github.com/leighmacdonald/mika/store"
	"github.com/leighmacdonald/mika/util"
	"sync"
)

const (
	driverName = "memory"
)

// TorrentStore is the memory backed store.TorrentStore implementation
type TorrentStore struct {
	sync.RWMutex
	torrents  map[model.InfoHash]model.Torrent
	whitelist []model.WhiteListClient
}

func NewTorrentStore() *TorrentStore {
	return &TorrentStore{
		RWMutex:   sync.RWMutex{},
		torrents:  map[model.InfoHash]model.Torrent{},
		whitelist: []model.WhiteListClient{},
	}
}

// Sync batch updates the backing store with the new TorrentStats provided
func (ts *TorrentStore) Sync(b map[model.InfoHash]model.TorrentStats) error {
	ts.Lock()
	defer ts.Unlock()
	for ih, stats := range b {
		t, found := ts.torrents[ih]
		if !found {
			// Deleted torrent before sync occurred
			continue
		}
		t.TotalUploaded += stats.Uploaded
		t.TotalDownloaded += stats.Downloaded
		t.TotalCompleted += stats.Snatches
		ts.torrents[ih] = t

	}
	return nil
}

// Conn always returns nil for in-memory store
func (ts *TorrentStore) Conn() interface{} {
	return nil
}

// WhiteListDelete removes a client from the global whitelist
func (ts *TorrentStore) WhiteListDelete(client model.WhiteListClient) error {
	ts.Lock()
	defer ts.Unlock()
	// Remove removes a peer from a slice
	for i := len(ts.whitelist) - 1; i >= 0; i-- {
		if ts.whitelist[i].ClientPrefix == client.ClientPrefix {
			ts.whitelist = append(ts.whitelist[:i], ts.whitelist[i+1:]...)
			return nil
		}
	}
	return consts.ErrInvalidClient
}

// WhiteListAdd will insert a new client prefix into the allowed clients list
func (ts *TorrentStore) WhiteListAdd(client model.WhiteListClient) error {
	ts.Lock()
	ts.whitelist = append(ts.whitelist, client)
	ts.Unlock()
	return nil
}

// WhiteListGetAll fetches all known whitelisted clients
func (ts *TorrentStore) WhiteListGetAll() ([]model.WhiteListClient, error) {
	ts.RLock()
	wl := ts.whitelist
	ts.RUnlock()
	return wl, nil
}

// Close will delete/free all the underlying torrent data
func (ts *TorrentStore) Close() error {
	ts.Lock()
	defer ts.Unlock()
	ts.torrents = make(map[model.InfoHash]model.Torrent)
	return nil
}

// Get returns the Torrent matching the infohash
func (ts *TorrentStore) Get(torrent *model.Torrent, hash model.InfoHash) error {
	ts.RLock()
	t, found := ts.torrents[hash]
	ts.RUnlock()
	if !found || t.IsDeleted {
		return consts.ErrInvalidInfoHash
	}
	*torrent = t
	return nil
}

// PeerStore is a memory backed store.PeerStore implementation
// TODO shard peer storage?
type PeerStore struct {
	sync.RWMutex
	peers map[model.InfoHash]model.Swarm
}

func NewPeerStore() *PeerStore {
	return &PeerStore{
		RWMutex: sync.RWMutex{},
		peers:   map[model.InfoHash]model.Swarm{},
	}
}

// Sync batch updates the backing store with the new PeerStats provided
func (ps *PeerStore) Sync(b map[model.PeerHash]model.PeerStats) error {
	ps.Lock()
	defer ps.Unlock()
	// TODO reduce the cyclic complexity of this
	for ph, stats := range b {
		ih := ph.InfoHash()
		pid := ph.PeerID()
		for idx, peer := range ps.peers[ih] {
			if pid == peer.PeerID {
				peer.Uploaded += stats.Uploaded
				peer.Downloaded += stats.Downloaded
				peer.Announces += stats.Announces
				peer.AnnounceLast = stats.LastAnnounce
				ps.peers[ih][idx] = peer
				break
			}
		}
	}
	return nil
}

// Reap will loop through the peers removing any stale entries from active swarms
func (ps *PeerStore) Reap() {
	ps.Lock()
	defer ps.Unlock()
	for _, swarm := range ps.peers {
		for _, peer := range swarm {
			if peer.Expired() {
				swarm.Remove(peer.PeerID)
			}
		}
	}
}

// Get will fetch the peer from the swarm if it exists
func (ps *PeerStore) Get(p *model.Peer, ih model.InfoHash, peerID model.PeerID) error {
	ps.RLock()
	defer ps.RUnlock()
	for _, peer := range ps.peers[ih] {
		if peer.PeerID == peerID {
			*p = peer
			return nil
		}
	}
	return consts.ErrInvalidPeerID
}

// Close flushes allocated memory
// TODO flush mem
func (ps *PeerStore) Close() error {
	ps.Lock()
	ps.peers = make(map[model.InfoHash]model.Swarm)
	ps.Unlock()
	return nil
}

// Add inserts a peer into the active swarm for the torrent provided
func (ps *PeerStore) Add(ih model.InfoHash, p model.Peer) error {
	ps.Lock()
	defer ps.Unlock()
	ps.peers[ih] = append(ps.peers[ih], p)
	return nil
}

// Update is a no-op for memory backed store
func (ps *PeerStore) Update(_ model.InfoHash, _ model.Peer) error {
	// no-op for in-memory store
	return nil
}

// Delete will remove a user from a torrents swarm
func (ps *PeerStore) Delete(ih model.InfoHash, p model.PeerID) error {
	ps.Lock()
	ps.peers[ih].Remove(p)
	ps.Unlock()
	return nil
}

// GetN will fetch peers for a torrents active swarm up to N users
func (ps *PeerStore) GetN(ih model.InfoHash, limit int) (model.Swarm, error) {
	ps.RLock()
	p, found := ps.peers[ih]
	ps.RUnlock()
	if !found {
		return nil, consts.ErrInvalidTorrentID
	}
	return p[0:util.MinInt(limit, len(p))], nil
}

// Add adds a new torrent to the memory store
func (ts *TorrentStore) Add(t model.Torrent) error {
	ts.RLock()
	_, found := ts.torrents[t.InfoHash]
	ts.RUnlock()
	if found {
		return consts.ErrDuplicate
	}
	ts.Lock()
	ts.torrents[t.InfoHash] = t
	ts.Unlock()
	return nil
}

// Delete will mark a torrent as deleted in the backing store.
// NOTE the memory store always permanently deletes the torrent
func (ts *TorrentStore) Delete(ih model.InfoHash, _ bool) error {
	ts.Lock()
	delete(ts.torrents, ih)
	ts.Unlock()
	return nil
}

type torrentDriver struct{}

// NewTorrentStore initialize a TorrentStore implementation using the memory backing store
func (td torrentDriver) NewTorrentStore(_ interface{}) (store.TorrentStore, error) {
	return NewTorrentStore(), nil
}

type peerDriver struct{}

// NewPeerStore initialize a NewPeerStore implementation using the memory backing store
func (pd peerDriver) NewPeerStore(_ interface{}) (store.PeerStore, error) {
	return NewPeerStore(), nil
}

// UserStore is the memory backed store.UserStore implementation
type UserStore struct {
	sync.RWMutex
	users map[string]model.User
}

func NewUserStore() *UserStore {
	return &UserStore{
		RWMutex: sync.RWMutex{},
		users:   map[string]model.User{},
	}
}

// Sync batch updates the backing store with the new UserStats provided
func (u *UserStore) Sync(b map[string]model.UserStats) error {
	u.Lock()
	defer u.Unlock()
	for passkey, stats := range b {
		user, found := u.users[passkey]
		if !found {
			// Deleted user
			continue
		}
		user.Announces += stats.Announces
		user.Downloaded += stats.Downloaded
		user.Uploaded += stats.Uploaded
		u.users[passkey] = user
	}
	return nil
}

// Add will add a new user to the backing store
func (u *UserStore) Add(usr model.User) error {
	u.Lock()
	u.users[usr.Passkey] = usr
	u.Unlock()
	return nil
}

// GetByPasskey will lookup and return the user via their passkey used as an identifier
// The errors returned for this method should be very generic and not reveal any info
// that could possibly help attackers gain any insight. All error cases MUST
// return ErrUnauthorized.
func (u *UserStore) GetByPasskey(usr *model.User, passkey string) error {
	u.RLock()
	user, found := u.users[passkey]
	u.RUnlock()
	if !found {
		return consts.ErrUnauthorized
	}
	*usr = user
	return nil
}

// GetByID returns a user matching the userId
func (u *UserStore) GetByID(user *model.User, userID uint32) error {
	u.RLock()
	defer u.RUnlock()
	for _, usr := range u.users {
		if usr.UserID == userID {
			*user = usr
			return nil
		}
	}
	return consts.ErrUnauthorized
}

// Delete removes a user from the backing store
func (u *UserStore) Delete(user model.User) error {
	u.Lock()
	delete(u.users, user.Passkey)
	u.Unlock()
	return nil
}

// Close will delete/free the underlying memory store
func (u *UserStore) Close() error {
	u.Lock()
	defer u.Unlock()
	u.users = make(map[string]model.User)
	return nil
}

type userDriver struct{}

// NewUserStore creates a new memory backed user store.
func (pd userDriver) NewUserStore(_ interface{}) (store.UserStore, error) {
	return NewUserStore(), nil
}

func init() {
	store.AddUserDriver(driverName, userDriver{})
	store.AddPeerDriver(driverName, peerDriver{})
	store.AddTorrentDriver(driverName, torrentDriver{})
}
