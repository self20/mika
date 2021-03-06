package client

import (
	"encoding/json"
	"fmt"
	h "github.com/leighmacdonald/mika/http"
	"github.com/leighmacdonald/mika/model"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"io/ioutil"
	"net/http"
	"time"
)

// Client is the API client implementation
type Client struct {
	host    string
	authKey string
	client  *http.Client
}

// New initializes an API client for the specified host
func New(host string, authKey string) *Client {
	c := h.NewClient(nil)
	return &Client{
		host:    host,
		authKey: authKey,
		client:  c,
	}
}

func (c *Client) u(path string) string {
	return fmt.Sprintf("http://%s%s", c.host, path)
}

func (c *Client) headers() map[string]string {
	if c.authKey == "" {
		return nil
	}
	return map[string]string{
		"Authorization": c.authKey,
	}
}

// TorrentDelete will delete the torrent matching the info_hash provided
func (c *Client) TorrentDelete(ih model.InfoHash) error {
	resp, err := h.DoRequest(c.client, "DELETE", c.u(fmt.Sprintf("/torrent/%s", ih.String())), nil, c.headers())
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return readStatus(resp)
	}
	log.Debugf("Torrent deleted successfully: %s", ih.String())
	return nil
}

// TorrentAdd add a new info_hash and associated name to be tracked
func (c *Client) TorrentAdd(ih model.InfoHash, name string) error {
	tar := h.TorrentAddRequest{
		InfoHash: ih.String(),
		Name:     name,
	}
	resp, err := h.DoRequest(c.client, "POST", c.u("/torrent"), tar, c.headers())
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return readStatus(resp)
	}
	log.Debugf("Torrent added successfully: %s", name)
	return nil
}

func readStatus(resp *http.Response) error {
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var sr h.StatusResp
	if err := json.Unmarshal(b, &sr); err != nil {
		return err
	}
	return sr
}

// UserDelete deletes the user matching the passkey provided
func (c *Client) UserDelete(passkey string) error {
	resp, err := h.DoRequest(c.client, "DELETE", c.u(fmt.Sprintf("/user/pk/%s", passkey)), nil, c.headers())
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return readStatus(resp)
	}
	log.Debugf("User deleted successfully: %s", passkey)
	return nil
}

// UserAdd creates a new user with the passkey provided
func (c *Client) UserAdd(passkey string) error {
	var req h.UserAddRequest
	req.Passkey = passkey
	resp, err := h.DoRequest(c.client, "POST", c.u("/user"), req, c.headers())
	if err != nil {
		return err
	}
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		var sr h.StatusResp
		if err := json.Unmarshal(b, &sr); err != nil {
			return err
		}
		return sr
	}
	var uar h.UserAddResponse
	return json.Unmarshal(b, &uar)
}

// Ping tests communication between the API server and the client
func (c *Client) Ping() error {
	const msg = "hello world"
	t0 := time.Now()
	resp, err := h.DoRequest(c.client, "POST", c.u("/ping"), h.PingRequest{Ping: msg}, c.headers())
	if err != nil {
		return errors.Wrapf(err, "Failed to make request")
	}
	b, err2 := ioutil.ReadAll(resp.Body)
	if err2 != nil {
		return errors.Wrap(err, "Failed to read ping body")
	}
	defer func() { _ = resp.Body.Close() }()
	var pong h.PingResponse
	if err := json.Unmarshal(b, &pong); err != nil {
		return err
	}
	log.Debugf("Ping successful: %s", time.Since(t0).String())
	if pong.Pong != msg {
		return errors.New("invalid response to message")
	}
	return nil
}
