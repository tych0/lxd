package lxd

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
)

// Client can talk to a lxd daemon.
type Client struct {
	config  Config
	Remote  *RemoteConfig
	http    http.Client
	baseURL string
	certf   string
	keyf    string
	cert    tls.Certificate
}

type ResponseType string

const (
	Sync = "sync"
	Async = "async"
	Error = "error"
)

type Jmap map[string]interface{}

func (m Jmap) getString(key string) (string, error) {
	if val, ok := m[key]; !ok {
		return "", fmt.Errorf("Response was missing `%s`", key)
	} else if val, ok := val.(string); !ok {
		return "", fmt.Errorf("`%s` was not a string", key)
	} else {
		return val, nil
	}
}

type Response struct {
	Type		ResponseType

	/* Valid only for Sync responses */
	Result		bool

	/* Valid only for Async responses */
	Operation	string

	/* Valid only for Error responses */
	Code		int

	/* Valid for Sync and Error responses */
	Metadata	Jmap
}

func ParseResponse(r *http.Response) (*Response, error) {
	defer r.Body.Close()
	ret := Response{}
	raw := Jmap{}

	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return nil, err
	}

	if key, ok := raw["type"]; !ok {
		return nil, fmt.Errorf("Response was missing `type`")
	} else if key == Sync {

		if result, err := raw.getString("result"); err != nil {
			return nil, err
		} else if result == "success" {
			ret.Result = true
		} else if result == "failure" {
			ret.Result = false
		} else {
			return nil, fmt.Errorf("Invalid result %s", result)
		}

		ret.Metadata = raw["metadata"].(map[string]interface{})

	} else if key == Async {

		if operation, err := raw.getString("operation"); err != nil {
			return nil, err
		} else {
			ret.Operation = operation
		}

	} else if key == Error {

		if code, err := raw.getString("code"); err != nil {
			return nil, err
		} else if i, err := strconv.Atoi(code); err != nil {
			return nil, err
		} else {
			ret.Code = i
			if ret.Code != r.StatusCode {
				return nil, fmt.Errorf("response codes don't match! %d %d", ret.Code, r.StatusCode)
			}
		}

		ret.Metadata = raw["metadata"].(map[string]interface{})

	} else {
		return nil, fmt.Errorf("Bad response type")
	}

	return &ret, nil
}

func ParseError(r *Response) error {
	if r.Type == Error {
		return fmt.Errorf("got error code %d", r.Code)
	}

	return nil
}

func read_my_cert() (string, string, error) {
	homedir := os.Getenv("HOME")
	if homedir == "" {
		return "", "", fmt.Errorf("Failed to find homedir")
	}
	certf := fmt.Sprintf("%s/.config/lxd/%s", homedir, "cert.pem")
	keyf := fmt.Sprintf("%s/.config/lxd/%s", homedir, "key.pem")

	_, err := os.Stat(certf)
	_, err2 := os.Stat(keyf)
	if err == nil && err2 == nil {
		return certf, keyf, nil
	}
	if err == nil {
		Debugf("%s already exists", certf)
		return "", "", err2
	}
	if err2 == nil {
		Debugf("%s already exists", keyf)
		return "", "", err
	}
	dir := fmt.Sprintf("%s/.config/lxd", homedir)
	err = os.MkdirAll(dir, 0750)
	if err != nil {
		return "", "", err
	}

	Debugf("creating cert: %s %s", certf, keyf)
	err = GenCert(certf, keyf)
	if err != nil {
		return "", "", err
	}
	return certf, keyf, nil
}


// NewClient returns a new lxd client.
func NewClient(config *Config, raw string) (*Client, string, error) {
	certf, keyf, err := read_my_cert()
	if err != nil {
		return nil, "", err
	}
	cert, err := tls.LoadX509KeyPair(certf, keyf)
	if err != nil {
		return nil, "", err
	}

	tlsconfig := &tls.Config{InsecureSkipVerify: true,
			ClientAuth: tls.RequireAnyClientCert,
			Certificates: []tls.Certificate{cert},
			MinVersion: tls.VersionTLS12,
			MaxVersion: tls.VersionTLS12,}
	tlsconfig.BuildNameToCertificate()

	tr := &http.Transport{
		TLSClientConfig: tlsconfig,
	}
	c := Client{
		config: *config,
		http:   http.Client{
		Transport: tr,
		// Added on Go 1.3. Wait until it's more popular.
		//Timeout: 10 * time.Second,
		},
	}

	c.certf = certf
	c.keyf = keyf
	c.cert = cert

	result := strings.SplitN(raw, ":", 2)
	var remote string
	var container string

	if len(result) == 1 {
		remote = config.DefaultRemote
		container = result[0]
	} else {
		remote = result[0]
		container = result[1]
	}

	// TODO: Here, we don't support configurable local remotes, we only
	// support the default local lxd at /var/lib/lxd/unix.socket.
	if remote == "" {
		c.baseURL = "http://unix.socket"
		c.http.Transport = &unixTransport
	} else if r, ok := config.Remotes[remote]; ok {
		c.baseURL = "https://" + r.Addr
		c.Remote = &r
	} else {
		return nil, "", fmt.Errorf("unknown remote name: %q", config.DefaultRemote)
	}
	if err := c.Ping(); err != nil {
		return nil, "", err
	}
	return &c, container, nil
}

/* This will be deleted once everything is ported to the new Response framework */
func (c *Client) getstr(base string, args map[string]string) (string, error) {
	vs := url.Values{}
	for k, v := range args {
		vs.Set(k, v)
	}

	resp, err := c.get(base + "?" + vs.Encode())
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

func (c *Client) getResponse(base string, args map[string]string) (*Response, error) {
	vs := url.Values{}
	for k, v := range args {
		vs.Set(k, v)
	}

	uri := fmt.Sprintf("/%s/%s", ApiVersion, base)

	resp, err := c.get(uri + "?" + vs.Encode())
	if err != nil {
		return nil, err
	}
	return ParseResponse(resp)
}

func (c *Client) get(elem ...string) (*http.Response, error) {
	url := c.url(elem...)
	Debugf("url is %s", url)
	resp, err := c.http.Get(url)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) url(elem ...string) string {
	return c.baseURL + path.Join(elem...)
}

var unixTransport = http.Transport{
	Dial: func(network, addr string) (net.Conn, error) {
		if addr != "unix.socket:80" {
			return nil, fmt.Errorf("non-unix-socket addresses not supported yet")
		}
		raddr, err := net.ResolveUnixAddr("unix", VarPath("unix.socket"))
		if err != nil {
			return nil, fmt.Errorf("cannot resolve unix socket address: %v", err)
		}
		return net.DialUnix("unix", nil, raddr)
	},
}

// Ping pings the daemon to see if it is up listening and working.
func (c *Client) Ping() error {
	Debugf("pinging the daemon")
	resp, err := c.getResponse("ping", nil)
	if err != nil {
		return err
	}

	if err := ParseError(resp); err != nil {
		return err
	}

	serverApiVersion, err := resp.Metadata.getString("api_compat")
	if err != nil {
		return err
	}

	if serverApiVersion != ApiVersion {
		return fmt.Errorf("api version mismatch: mine: %q, daemon: %q", ApiVersion, serverApiVersion)
	}
	Debugf("pong received")
	return nil
}

func (c *Client) AmTrusted() bool {
	data, err := c.getstr("/ping", nil)
	if err != nil {
		return false
	}

	datav := strings.Split(string(data), " ")
	if datav[1] == "trusted" {
		return true
	}
	return false
}

func (c *Client) List() (string, error) {
	data, err := c.getstr("/list", nil)
	if err != nil {
		return "fail", err
	}
	return data, err
}

func (c *Client) AddCertToServer() (string, error) {
	data, err := c.getstr("/trust/add", nil)
	if err != nil {
		return "fail", err
	}
	return data, err
}

func (c *Client) Create(name string, distro string, release string, arch string) (string, error) {
	data, err := c.getstr("/create", map[string]string{
		"name":    name,
		"distro":  distro,
		"release": release,
		"arch":    arch,
	})
	if err != nil {
		return "fail", err
	}
	return data, err
}

func (c *Client) Shell(name string, cmd string, secret string) (string, error) {
	data, err := c.getstr("/shell", map[string]string{
		"name":    name,
		"command": cmd,
		"secret":  secret,
	})
	if err != nil {
		return "fail", err
	}
	return data, err
}

// Call a function in the lxd API by name (i.e. this has nothing to do with
// the parameter passing schemed :)
func (c *Client) CallByName(function string, name string) (string, error) {
	data, err := c.getstr("/"+function, map[string]string{"name": name})
	if err != nil {
		return "", err
	}
	return data, err
}

func (c *Client) Delete(name string) (string, error) {
	return c.CallByName("delete", name)
}

func (c *Client) Start(name string) (string, error) {
	return c.CallByName("start", name)
}

func (c *Client) Stop(name string) (string, error) {
	return c.CallByName("stop", name)
}

func (c *Client) Restart(name string) (string, error) {
	return c.CallByName("restart", name)
}
