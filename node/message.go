package node

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/rs/zerolog/log"
)

var jsonEscapedZero = []byte(`\u0000`)

// PingArgs get passed to the Ping command
type PingArgs struct {
	Addr string
}

// AddArgs get passed to the Add command
type AddArgs struct {
	Path      string
	ChunkSize int
}

// StatusArgs get passed to the Status command
type StatusArgs struct {
	Verbose bool
}

// PackArgs are passed to the Pack command
type PackArgs struct {
	Archive bool
}

// QuoteArgs are passed to the quote command
type QuoteArgs struct {
	Ref       string
	StorageRF int // StorageRF is the replication factor or number of miners we will try to store with
	Duration  time.Duration
	MaxPrice  uint64
}

// PushArgs are passed to the Push command
type PushArgs struct {
	Ref       string // Ref is the root CID of the archive to push to remote storage
	NoCache   bool
	CacheOnly bool
	CacheRF   int // CacheRF is the cache replication factor or number of cache provider will request
	StorageRF int // StorageRF if the replication factor for storage
	Duration  time.Duration
	Miners    map[string]bool
}

// GetArgs get passed to the Get command
type GetArgs struct {
	Cid      string
	Segments []string
	Sel      string
	Out      string
	Timeout  int
	Verbose  bool
	Miner    string
}

// Command is a message sent from a client to the daemon
type Command struct {
	Ping   *PingArgs
	Add    *AddArgs
	Status *StatusArgs
	Pack   *PackArgs
	Quote  *QuoteArgs
	Push   *PushArgs
	Get    *GetArgs
}

// PingResult is sent in the notify message to give us the info we requested
type PingResult struct {
	ID             string   // Host's peer ID
	Addrs          []string // Addresses the host is listening on
	Peers          []string // Peers currently connected to the node (local daemon only)
	LatencySeconds float64
	Err            string
}

// AddResult gives us feedback on the result of the Add request
type AddResult struct {
	Cid       string
	Size      string
	NumBlocks int
	Err       string
}

// StatusResult gives us the result of status request to pring
type StatusResult struct {
	Output string
	Err    string
}

// PackResult gives us feedback on the result of the Commit operation
type PackResult struct {
	DataCID   string
	DataSize  int64
	PieceCID  string
	PieceSize int64
	Err       string
}

// QuoteResult returns the output of the Quote request
type QuoteResult struct {
	Ref    string
	Quotes map[string]string
	Err    string
}

// PushResult is feedback on the push operation
type PushResult struct {
	Miners []string
	Deals  []string
	Caches []string
	Err    string
}

// GetResult gives us feedback on the result of the Get request
type GetResult struct {
	DealID          string
	TotalSpent      string
	TotalPrice      string
	PieceSize       string
	PricePerByte    string
	UnsealPrice     string
	DiscLatSeconds  float64
	TransLatSeconds float64
	Local           bool
	Err             string
}

// Notify is a message sent from the daemon to the client
type Notify struct {
	PingResult   *PingResult
	AddResult    *AddResult
	StatusResult *StatusResult
	PackResult   *PackResult
	QuoteResult  *QuoteResult
	PushResult   *PushResult
	GetResult    *GetResult
}

// CommandServer receives commands on the daemon side and executes them
type CommandServer struct {
	n             *node                // the ipfs node we are controlling
	sendNotifyMsg func(jsonMsg []byte) // send a notification message
}

func NewCommandServer(ipfs *node, sendNotifyMsg func(b []byte)) *CommandServer {
	return &CommandServer{
		n:             ipfs,
		sendNotifyMsg: sendNotifyMsg,
	}
}

func (cs *CommandServer) GotMsgBytes(ctx context.Context, b []byte) error {
	cmd := &Command{}
	if len(b) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, cmd); err != nil {
		return err
	}
	return cs.GotMsg(ctx, cmd)
}

func (cs *CommandServer) GotMsg(ctx context.Context, cmd *Command) error {
	if c := cmd.Ping; c != nil {
		cs.n.Ping(ctx, c.Addr)
		return nil
	}
	if c := cmd.Add; c != nil {
		cs.n.Add(ctx, c)
		return nil
	}
	if c := cmd.Status; c != nil {
		cs.n.Status(ctx, c)
		return nil
	}
	if c := cmd.Pack; c != nil {
		cs.n.Pack(ctx, c)
		return nil
	}
	if c := cmd.Quote; c != nil {
		cs.n.Quote(ctx, c)
		return nil
	}
	if c := cmd.Push; c != nil {
		// push requests are usually quite long so we don't block the thread so users
		// can keep adding to the workdag while their previous commit is uploading for example
		go cs.n.Push(ctx, c)
		return nil
	}
	if c := cmd.Get; c != nil {
		// Get requests can be quite long and we don't want to block other commands
		go cs.n.Get(ctx, c)
		return nil
	}
	return fmt.Errorf("CommandServer: no command specified")
}

func (cs *CommandServer) send(n Notify) {
	b, err := json.Marshal(n)
	if err != nil {
		log.Fatal().Err(err).Interface("n", n).Msg("Failed json.Marshal(notify)")
	}
	if bytes.Contains(b, jsonEscapedZero) {
		log.Error().Msg("[unexpected] zero byte in BackendServer.send notify message")
	}
	cs.sendNotifyMsg(b)
}

// CommandClient sends commands to a daemon process
type CommandClient struct {
	sendCommandMsg func(jsonb []byte)
	notify         func(Notify)
}

func NewCommandClient(sendCommandMsg func(jsonb []byte)) *CommandClient {
	return &CommandClient{
		sendCommandMsg: sendCommandMsg,
	}
}

func (cc *CommandClient) GotNotifyMsg(b []byte) {
	if len(b) == 0 {
		// not interesting
		return
	}
	if bytes.Contains(b, jsonEscapedZero) {
		log.Error().Msg("[unexpected] zero byte in BackendClient.GotNotifyMsg message")
	}
	n := Notify{}
	if err := json.Unmarshal(b, &n); err != nil {
		log.Fatal().Err(err).Int("len", len(b)).Msg("BackendClient.Notify: cannot decode message")
	}
	if cc.notify != nil {
		cc.notify(n)
	}
}

func (cc *CommandClient) send(cmd Command) {
	b, err := json.Marshal(cmd)
	if err != nil {
		log.Error().Err(err).Msg("Failed json.Marshal(cmd)")
	}
	if bytes.Contains(b, jsonEscapedZero) {
		log.Error().Err(err).Msg("[unexpected] zero byte in CommandClient.send")
	}
	cc.sendCommandMsg(b)
}

func (cc *CommandClient) Ping(addr string) {
	cc.send(Command{Ping: &PingArgs{Addr: addr}})
}

func (cc *CommandClient) Add(args *AddArgs) {
	cc.send(Command{Add: args})
}

func (cc *CommandClient) Status(args *StatusArgs) {
	cc.send(Command{Status: args})
}

func (cc *CommandClient) Pack(args *PackArgs) {
	cc.send(Command{Pack: args})
}

func (cc *CommandClient) Quote(args *QuoteArgs) {
	cc.send(Command{Quote: args})
}

func (cc *CommandClient) Push(args *PushArgs) {
	cc.send(Command{Push: args})
}

func (cc *CommandClient) Get(args *GetArgs) {
	cc.send(Command{Get: args})
}

func (cc *CommandClient) SetNotifyCallback(fn func(Notify)) {
	cc.notify = fn
}

// MaxMessageSize is the maximum message size, in bytes.
const MaxMessageSize = 10 << 20

func ReadMsg(r io.Reader) ([]byte, error) {
	cb := make([]byte, 4)
	_, err := io.ReadFull(r, cb)
	if err != nil {
		return nil, err
	}
	n := binary.LittleEndian.Uint32(cb)
	if n > MaxMessageSize {
		return nil, fmt.Errorf("ReadMsg: message too large: %d bytes", n)
	}
	b := make([]byte, n)
	nn, err := io.ReadFull(r, b)
	if err != nil {
		return nil, err
	}
	if nn != int(n) {
		return nil, fmt.Errorf("ReadMsg: expected %d bytes, got %d", n, nn)
	}
	return b, nil
}

func WriteMsg(w io.Writer, b []byte) error {

	cb := make([]byte, 4)
	if len(b) > MaxMessageSize {
		return fmt.Errorf("WriteMsg: message too large: %d bytes", len(b))
	}
	binary.LittleEndian.PutUint32(cb, uint32(len(b)))
	n, err := w.Write(cb)
	if err != nil {
		return err
	}
	if n != 4 {
		return fmt.Errorf("WriteMsg: short write: %d bytes (wanted 4)", n)
	}
	n, err = w.Write(b)
	if err != nil {
		return err
	}
	if n != len(b) {
		return fmt.Errorf("WriteMsg: short write: %d bytes (wanted %d)", n, len(b))
	}
	return nil
}
