package tm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/log"
	"github.com/tendermint/tendermint/rpc/jsonrpc/types"
)

// ------------解析传入的地址，兼容多种协议，把这些地址转换成 HTTP/WebSocket/Dial 能使用的格式，定义 RPC 调用接口----------
const (
	protoHTTP  = "http"
	protoHTTPS = "https"
	protoWSS   = "wss"
	protoWS    = "ws"   //ws://localhost:26657/websocket
	protoTCP   = "tcp"  //tcp://localhost:26657
	protoUNIX  = "unix" //unix:///tmp/rpc.sock
)

type parsedURL struct {
	url.URL
	isUnixSocket bool
}

// Parse URL and set defaults
func newParsedURL(remoteAddr string) (*parsedURL, error) {
	u, err := url.Parse(remoteAddr)
	if err != nil {
		return nil, err
	}

	// default to tcp if nothing specified
	if u.Scheme == "" {
		u.Scheme = protoTCP
	}
	pu := &parsedURL{
		URL:          *u,
		isUnixSocket: false,
	}

	if u.Scheme == protoUNIX {
		pu.isUnixSocket = true
	}

	return pu, nil
}

// SetDefaultSchemeHTTP Change protocol to HTTP for unknown protocols and TCP protocol - useful for RPC connections
func (u *parsedURL) SetDefaultSchemeHTTP() {
	switch u.Scheme {
	case protoHTTP, protoHTTPS, protoWS, protoWSS:
	// known protocols not changed
	default:
		// default to http for unknown protocols (ex. tcp)
		u.Scheme = protoHTTP
	}
}

// GetHostWithPath Get full address without the protocol - useful for Dialer connections
func (u *parsedURL) GetHostWithPath() string {
	return u.Host + u.EscapedPath()
}

// GetTrimmedHostWithPath Get a trimmed address - useful for WS connections
func (u parsedURL) GetTrimmedHostWithPath() string {
	if !u.isUnixSocket {
		return u.GetHostWithPath()
	}
	// unix:///tmp/socket .tmp.socket
	return strings.ReplaceAll(u.GetHostWithPath(), "/", ".")
}

// GetDialAddress returns the endpoint to dial for the parsed URL
func (u parsedURL) GetDialAddress() string {
	// if it's not a unix socket we return the host, example: localhost:443
	if !u.isUnixSocket {
		return u.Host
	}
	// otherwise we return the path of the unix socket, ex /tmp/socket
	return u.GetHostWithPath()
}

// GetTrimmedURL Get a trimmed address with protocol - useful as address in RPC connections
// unix:///tmp/socket 返回 unix://.tmp.socket
func (u parsedURL) GetTrimmedURL() string {
	return u.Scheme + "://" + u.GetTrimmedHostWithPath()
}

//-------------------------------------------------------------

// HTTPClient is a common interface for JSON-RPC HTTP clients.
type HTTPClient interface {
	Call(ctx context.Context, method string, params map[string]interface{}, result interface{}) (interface{}, error)
}

// Caller implementers can facilitate calling the JSON-RPC endpoint.
type Caller interface {
	Call(ctx context.Context, method string, params map[string]interface{}, result interface{}) (interface{}, error)
}

//-------------------------------------------------------------

// Client is a JSON-RPC client, which sends POST HTTP requests to the remote server.
type Client struct {
	address  string
	username string
	password string

	client *http.Client

	mtx       sync.Mutex
	nextReqID int
}

//var _ HTTPClient = (*Client)(nil)
//var _ Caller = (*Client)(nil)
//var _ Caller = (*RequestBatch)(nil)

func NewWithHTTPClient(remote string, client *http.Client) (*Client, error) {
	if client == nil {
		panic("nil http.Client provided")
	}

	parsedURL, err := newParsedURL(remote)
	if err != nil {
		return nil, fmt.Errorf("invalid remote %s: %s", remote, err)
	}
	parsedURL.SetDefaultSchemeHTTP()

	address := parsedURL.GetTrimmedURL()
	username := parsedURL.User.Username()
	password, _ := parsedURL.User.Password()

	rpcClient := &Client{
		address:  address,
		username: username,
		password: password,
		client:   client,
	}
	return rpcClient, nil
}

func (c *Client) nextRequestID() types.JSONRPCIntID {
	c.mtx.Lock()
	id := c.nextReqID
	c.nextReqID++
	c.mtx.Unlock()
	return types.JSONRPCIntID(id)
}

// Call issues a POST HTTP request. Requests are JSON encoded. Content-Type: application/json.
func (c *Client) Call(
	ctx context.Context,
	method string,
	params map[string]interface{},
	result interface{},
) (interface{}, error) {
	id := c.nextRequestID()

	request, err := types.MapToRequest(id, method, params)
	if err != nil {
		return nil, fmt.Errorf("failed to encode params: %w", err)
	}

	requestBytes, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	requestBuf := bytes.NewBuffer(requestBytes)
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.address, requestBuf)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	httpRequest.Header.Set("Content-Type", "application/json")
	if c.username != "" || c.password != "" {
		httpRequest.SetBasicAuth(c.username, c.password)
	}

	httpResponse, err := c.client.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("post failed: %w", err)
	}

	defer func() {
		if err := httpResponse.Body.Close(); err != nil {
			log.Error("Failed to close connection", "err", err)
		}
	}()

	responseBytes, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return unmarshalResponseBytes(responseBytes, id, result)
}

//------------------------------------------------------------------------------------

// jsonRPCBufferedRequest encapsulates a single buffered request, as well as its anticipated response structure.
type jsonRPCBufferedRequest struct {
	request types.RPCRequest
	result  interface{} // The result will be deserialized into this object.
}

func (c *Client) sendBatch(ctx context.Context, requests []*jsonRPCBufferedRequest) ([]interface{}, error) {
	reqs := make([]types.RPCRequest, 0, len(requests))
	results := make([]interface{}, 0, len(requests))
	for _, req := range requests {
		reqs = append(reqs, req.request)
		results = append(results, req.result)
	}
	requestBytes, err := json.Marshal(reqs)
	if err != nil {
		return nil, fmt.Errorf("json marshal: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.address, bytes.NewBuffer(requestBytes))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}

	httpRequest.Header.Set("Content-Type", "application/json")
	if c.username != "" || c.password != "" {
		httpRequest.SetBasicAuth(c.username, c.password)
	}

	httpResponse, err := c.client.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("post: %w", err)
	}

	defer func() {
		if err := httpResponse.Body.Close(); err != nil {
			log.Error("Failed to close connection", "err", err)
		}
	}()

	responseBytes, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	// collect ids to check responses IDs in unmarshalResponseBytesArray
	ids := make([]types.JSONRPCIntID, len(requests))
	for i, req := range requests {
		ids[i] = req.request.ID.(types.JSONRPCIntID)
	}

	return unmarshalResponseBytesArray(responseBytes, ids, results)
}

//------------======================================

// RequestBatch allows us to buffer multiple request/response structures into a single batch request. Note that this batch acts like a FIFO queue, and
// is thread-safe.
type RequestBatch struct {
	client *Client

	mtx      sync.Mutex
	requests []*jsonRPCBufferedRequest
}

// Count returns the number of enqueued requests waiting to be sent.
func (b *RequestBatch) Count() int {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	return len(b.requests)
}

func (b *RequestBatch) enqueue(req *jsonRPCBufferedRequest) {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	b.requests = append(b.requests, req)
}

// Clear empties out the request batch.
func (b *RequestBatch) Clear() int {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	return b.clear()
}

func (b *RequestBatch) clear() int {
	count := len(b.requests)
	b.requests = make([]*jsonRPCBufferedRequest, 0)
	return count
}

// Send will attempt to send the current batch of enqueued requests, and then will clear out the requests once done.
func (b *RequestBatch) Send(ctx context.Context) ([]interface{}, error) {
	b.mtx.Lock()
	defer func() {
		b.clear()
		b.mtx.Unlock()
	}()
	return b.client.sendBatch(ctx, b.requests)
}

// Call enqueues a request to call the given RPC method with the specified parameters, in the same way that the `Client.Call` function would.
func (b *RequestBatch) Call(
	_ context.Context,
	method string,
	params map[string]interface{},
	result interface{},
) (interface{}, error) {
	id := b.client.nextRequestID()
	request, err := types.MapToRequest(id, method, params)
	if err != nil {
		return nil, err
	}
	b.enqueue(&jsonRPCBufferedRequest{request: request, result: result})
	return result, nil
}

//-------------------------------------------------------------

func makeHTTPDialer(remoteAddr string) (func(string, string) (net.Conn, error), error) {
	u, err := newParsedURL(remoteAddr)
	if err != nil {
		return nil, err
	}

	protocol := u.Scheme

	// accept http(s) as an alias for tcp
	switch protocol {
	case protoHTTP, protoHTTPS:
		protocol = protoTCP
	}

	dialFn := func(proto, addr string) (net.Conn, error) {
		return net.Dial(protocol, u.GetDialAddress())
	}

	return dialFn, nil
}
