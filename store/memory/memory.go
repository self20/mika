package memory

import (
	"mika/consts"
	"mika/model"
	"mika/store"
	"sync"
)

const (
	driverName = "memory"
)

// TorrentStore is the memory backed store.TorrentStore implementation
type TorrentStore struct {
	sync.RWMutex
	torrents map[model.InfoHash]*model.Torrent
}

// Close will delete/free all the underlying torrent data
func (ts *TorrentStore) Close() error {
	ts.Lock()
	ts.torrents = map[model.InfoHash]*model.Torrent{}
	ts.Unlock()
	return nil
}

// GetTorrent returns the Torrent matching the infohash
func (ts *TorrentStore) GetTorrent(hash model.InfoHash) (*model.Torrent, error) {
	ts.RLock()
	t, found := ts.torrents[hash]
	ts.RUnlock()
	if !found || t.IsDeleted {
		return nil, consts.ErrInvalidInfoHash
	}
	return t, nil
}

// PeerStore is a memory backed store.PeerStore implementation
// TODO shard peer storage
type PeerStore struct {
	sync.RWMutex
	peers map[model.InfoHash]model.Swarm
}

// GetPeer will fetch the peer from the swarm if it exists
func (ps *PeerStore) GetPeer(ih model.InfoHash, p model.PeerID) (*model.Peer, error) {
	ps.RLock()
	defer ps.RUnlock()
	for _, peer := range ps.peers[ih] {
		if peer.PeerID == p {
			return peer, nil
		}
	}
	return nil, consts.ErrInvalidPeerID
}

// Close flushes allocated memory
// TODO flush mem
func (ps *PeerStore) Close() error {
	ps.Lock()
	ps.peers = make(map[model.InfoHash]model.Swarm)
	ps.Unlock()
	return nil
}

// AddPeer inserts a peer into the active swarm for the torrent provided
func (ps *PeerStore) AddPeer(ih model.InfoHash, p *model.Peer) error {
	ps.Lock()
	ps.peers[ih] = append(ps.peers[ih], p)
	ps.Unlock()
	return nil
}

// UpdatePeer is a no-op for memory backed store
func (ps *PeerStore) UpdatePeer(_ model.InfoHash, _ *model.Peer) error {
	// no-op for in-memory store
	return nil
}

// DeletePeer will remove a user from a torrents swarm
func (ps *PeerStore) DeletePeer(ih model.InfoHash, p *model.Peer) error {
	ps.Lock()
	ps.peers[ih].Remove(p)
	ps.Unlock()
	return nil
}

// GetPeers will fetch peers for a torrents active swarm up to N users
func (ps *PeerStore) GetPeers(ih model.InfoHash, limit int) (model.Swarm, error) {
	ps.RLock()
	p, found := ps.peers[ih]
	ps.RUnlock()
	if !found {
		return nil, consts.ErrInvalidTorrentID
	}
	return p[0:limit], nil
}

// GetScrape returns scrape data for the torrent provided
func (ps *PeerStore) GetScrape(_ model.InfoHash) {
	panic("implement me")
}

// AddTorrent adds a new torrent to the memory store
func (ts *TorrentStore) AddTorrent(t *model.Torrent) error {
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

// DeleteTorrent will mark a torrent as deleted in the backing store.
// NOTE the memory store always permanently deletes the torrent
func (ts *TorrentStore) DeleteTorrent(t *model.Torrent, _ bool) error {
	ts.Lock()
	delete(ts.torrents, t.InfoHash)
	ts.Unlock()
	return nil
}

type torrentDriver struct{}

// NewTorrentStore initialize a TorrentStore implementation using the memory backing store
func (td torrentDriver) NewTorrentStore(_ interface{}) (store.TorrentStore, error) {
	return &TorrentStore{
		sync.RWMutex{},
		make(map[model.InfoHash]*model.Torrent),
	}, nil
}

type peerDriver struct{}

// NewPeerStore initialize a NewPeerStore implementation using the memory backing store
func (pd peerDriver) NewPeerStore(_ interface{}) (store.PeerStore, error) {
	return &PeerStore{
		sync.RWMutex{},
		make(map[model.InfoHash]model.Swarm),
	}, nil
}

// UserStore is the memory backed store.UserStore implementation
type UserStore struct {
	sync.RWMutex
	users map[string]*model.User
}

// AddUser will add a new user to the backing store
func (u *UserStore) AddUser(usr *model.User) error {
	u.Lock()
	u.users[usr.Passkey] = usr
	u.Unlock()
	return nil
}

// GetUserByPasskey will lookup and return the user via their passkey used as an identifier
// The errors returned for this method should be very generic and not reveal any info
// that could possibly help attackers gain any insight. All error cases MUST
// return ErrUnauthorized.
func (u *UserStore) GetUserByPasskey(passkey string) (*model.User, error) {
	u.RLock()
	user, found := u.users[passkey]
	u.RUnlock()
	if !found {
		return nil, consts.ErrUnauthorized
	}
	return user, nil
}

// GetUserByID returns a user matching the userId
func (u *UserStore) GetUserByID(userID uint32) (*model.User, error) {
	u.RLock()
	defer u.RUnlock()
	for _, usr := range u.users {
		if usr.UserID == userID {
			return usr, nil
		}
	}
	return nil, consts.ErrUnauthorized
}

// DeleteUser removes a user from the backing store
func (u *UserStore) DeleteUser(user *model.User) error {
	u.Lock()
	delete(u.users, user.Passkey)
	u.Unlock()
	return nil
}

// Close will delete/free the underlying memory store
func (u *UserStore) Close() error {
	u.Lock()
	u.users = make(map[string]*model.User)
	u.Unlock()
	return nil
}

type userDriver struct{}

// NewUserStore creates a new memory backed user store.
func (pd userDriver) NewUserStore(_ interface{}) (store.UserStore, error) {
	return &UserStore{
		sync.RWMutex{},
		make(map[string]*model.User),
	}, nil
}

func init() {
	store.AddUserDriver(driverName, userDriver{})
	store.AddPeerDriver(driverName, peerDriver{})
	store.AddTorrentDriver(driverName, torrentDriver{})
}
