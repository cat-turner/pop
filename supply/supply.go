package supply

import (
	"bufio"
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/filecoin-project/go-address"
	cborutil "github.com/filecoin-project/go-cbor-util"
	datatransfer "github.com/filecoin-project/go-data-transfer"
	"github.com/filecoin-project/go-multistore"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/namespace"
	"github.com/ipld/go-ipld-prime"
	basicnode "github.com/ipld/go-ipld-prime/node/basic"
	"github.com/ipld/go-ipld-prime/traversal/selector"
	"github.com/ipld/go-ipld-prime/traversal/selector/builder"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/mux"
	"github.com/libp2p/go-libp2p-core/network"
	peer "github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/protocol"
)

// ErrNoPeers when no peers are available to get or send supply to
var ErrNoPeers = fmt.Errorf("no peers available for supply")

// MaxReceiverCount is the maximum number of peers one can dispatch to
// we currently do not allow tweaking that number manually so users aren't tempted to DDOS the network
const MaxReceiverCount = 7

// RequestProtocol labels our network for announcing new content to the network
const RequestProtocol = "/myel/supply/dispatch/1.0"

func protoRegions(proto string, regions []Region) []protocol.ID {
	var pls []protocol.ID
	for _, r := range regions {
		pls = append(pls, protocol.ID(fmt.Sprintf("%s/%s", proto, r.Name)))
	}
	return pls
}

// Request describes the content to pull
type Request struct {
	PayloadCID cid.Cid
	Size       uint64
}

// Type defines AddRequest as a datatransfer voucher for pulling the data from the request
func (Request) Type() datatransfer.TypeIdentifier {
	return "DispatchRequestVoucher"
}

// PRecord is a provider <> cid mapping for recording who is storing what content
type PRecord struct {
	Provider   peer.ID
	PayloadCID cid.Cid
}

// Response is an async collection of confirmations from data transfers to cache providers
// it also provides the number of peers we messaged
type Response struct {
	recordChan chan PRecord
	unsub      datatransfer.Unsubscribe

	Count int
}

// Next returns the next record from a new cache
func (r *Response) Next(ctx context.Context) (PRecord, error) {
	select {
	case r := <-r.recordChan:
		return r, nil
	case <-ctx.Done():
		return PRecord{}, ctx.Err()
	}
}

// Close stops listening for cache confirmations
func (r *Response) Close() {
	r.unsub()
	close(r.recordChan)
}

// Network handles all the different messaging protocols
// related to content supply
type Network struct {
	host      host.Host
	receiver  StreamReceiver
	protocols []protocol.ID
}

// NewNetwork creates a new Network instance
func NewNetwork(h host.Host, regions []Region) *Network {
	sn := &Network{
		host:      h,
		protocols: protoRegions(RequestProtocol, regions),
	}
	return sn
}

// NewRequestStream to send AddRequest messages to
func (n *Network) NewRequestStream(dest peer.ID) (RequestStreamer, error) {
	s, err := n.host.NewStream(context.Background(), dest, n.protocols...)
	if err != nil {
		return nil, err
	}
	buffered := bufio.NewReaderSize(s, 16)
	return &requestStream{p: dest, rw: s, buffered: buffered}, nil
}

// SetDelegate assigns a handler for all the protocols
func (n *Network) SetDelegate(sr StreamReceiver) {
	n.receiver = sr
	for _, proto := range n.protocols {
		n.host.SetStreamHandler(proto, n.handleStream)
	}
}

func (n *Network) handleStream(s network.Stream) {
	if n.receiver == nil {
		fmt.Printf("no receiver set")
		s.Reset()
		return
	}
	remotePID := s.Conn().RemotePeer()
	buffered := bufio.NewReaderSize(s, 16)
	ns := &requestStream{remotePID, s, buffered}
	n.receiver.HandleRequest(ns)
}

// StreamReceiver will read the stream and do something in response
type StreamReceiver interface {
	HandleRequest(RequestStreamer)
}

// RequestStreamer reads AddRequest structs from a muxed stream
type RequestStreamer interface {
	ReadRequest() (Request, error)
	WriteRequest(Request) error
	OtherPeer() peer.ID
	Close() error
}

type requestStream struct {
	p        peer.ID
	rw       mux.MuxedStream
	buffered *bufio.Reader
}

func (a *requestStream) ReadRequest() (Request, error) {
	var m Request
	if err := m.UnmarshalCBOR(a.buffered); err != nil {
		return Request{}, err
	}
	return m, nil
}

func (a *requestStream) WriteRequest(m Request) error {
	return cborutil.WriteCborRPC(a.rw, &m)
}

func (s *requestStream) Close() error {
	return s.rw.Close()
}

func (s *requestStream) OtherPeer() peer.ID {
	return s.p
}

type handler struct {
	ms *multistore.MultiStore
	dt datatransfer.Manager
	s  *Store
}

// AllSelector is the default selector that reaches all the blocks
func AllSelector() ipld.Node {
	ssb := builder.NewSelectorSpecBuilder(basicnode.Prototype.Any)
	return ssb.ExploreRecursive(selector.RecursionLimitNone(),
		ssb.ExploreAll(ssb.ExploreRecursiveEdge())).Node()
}

// HandleRequest pulls the blocks from the peer upon receiving the request
func (h *handler) HandleRequest(stream RequestStreamer) {
	defer stream.Close()

	req, err := stream.ReadRequest()
	if err != nil {
		return
	}

	// TODO: run custom logic to validate the presence of a storage deal for this block
	// we may need to request deal info in the message
	// + check if we have room to store it

	// Create a new store to receive our new blocks
	// It will be automatically picked up in the TransportConfigurer
	storeID := h.ms.Next()
	err = h.s.PutRecord(req.PayloadCID, &ContentRecord{Labels: map[string]string{
		KStoreID: fmt.Sprintf("%d", storeID),
		KSize:    fmt.Sprintf("%d", req.Size),
	}})
	if err != nil {
		return
	}
	_, err = h.dt.OpenPullDataChannel(context.TODO(), stream.OtherPeer(), &req, req.PayloadCID, AllSelector())
	if err != nil {
		return
	}
}

// Supply keeps track of the content we store and provide on the network
// its role is to always seek and supply new and more efficient content to store
type Supply struct {
	h          host.Host
	dt         datatransfer.Manager
	ms         *multistore.MultiStore
	net        *Network
	store      *Store
	validation *Validator
	regions    []Region
}

// New instance of the SupplyManager
func New(
	h host.Host,
	dt datatransfer.Manager,
	ds datastore.Batching,
	ms *multistore.MultiStore,
	regions []Region,
) *Supply {
	store := &Store{namespace.Wrap(ds, datastore.NewKey("/supply"))}
	v := &Validator{
		auth: make(map[cid.Cid]*peer.Set),
	}
	s := &Supply{
		h:          h,
		dt:         dt,
		ms:         ms,
		net:        NewNetwork(h, regions),
		store:      store,
		regions:    regions,
		validation: v,
	}
	s.dt.RegisterVoucherType(&Request{}, v)
	s.dt.RegisterTransportConfigurer(&Request{}, TransportConfigurer(s))
	s.net.SetDelegate(&handler{ms, dt, store})

	// TODO: clean this up
	dt.SubscribeToEvents(func(event datatransfer.Event, channelState datatransfer.ChannelState) {
		if event.Code == datatransfer.Error && channelState.Recipient() == h.ID() {
			// If transfers fail and we're the recipient we need to remove it from our index
			store.RemoveRecord(channelState.BaseCID())
		}
	})
	return s
}

// Register a new content record in our supply
func (s *Supply) Register(key cid.Cid, sid multistore.StoreID) error {
	// Store a record of the content in our supply
	return s.store.PutRecord(key, &ContentRecord{Labels: map[string]string{
		KStoreID: fmt.Sprintf("%d", sid),
	}})
}

// Dispatch requests to the network until we have propagated the content to enough peers
func (s *Supply) Dispatch(r Request) (*Response, error) {
	res := &Response{
		recordChan: make(chan PRecord),
	}

	// listen for datatransfer events to identify the peers who pulled the content
	res.unsub = s.dt.SubscribeToEvents(func(event datatransfer.Event, chState datatransfer.ChannelState) {
		if chState.Status() == datatransfer.Completed {
			root := chState.BaseCID()
			if root != r.PayloadCID {
				return
			}
			// The recipient is the provider who received our content
			rec := chState.Recipient()
			res.recordChan <- PRecord{
				Provider:   rec,
				PayloadCID: root,
			}
		}
	})

	// Select the providers we want to send to
	providers, err := s.selectProviders()
	if err != nil {
		return res, err
	}
	// Authorize the transfer
	for _, p := range providers {
		s.validation.Authorize(r.PayloadCID, p)
	}
	res.Count = len(providers)
	s.sendAllRequests(r, providers)
	return res, nil
}

func (s *Supply) selectProviders() ([]peer.ID, error) {
	var peers []peer.ID
	// Get the current connected peers
	for _, pconn := range s.h.Network().Conns() {
		pid := pconn.RemotePeer()
		// Make sure we don't add ourselves
		if pid != s.h.ID() {
			// Make sure our peer supports the retrieval dispatch protocol
			var protos []string
			for _, p := range protoRegions(RequestProtocol, s.regions) {
				protos = append(protos, string(p))
			}
			supported, err := s.h.Peerstore().SupportsProtocols(
				pid,
				protos...,
			)
			if err != nil || len(supported) == 0 {
				continue
			}
			peers = append(peers, pid)
		}
	}

	if len(peers) == 0 {
		return nil, ErrNoPeers
	}
	// If we have less peers we adjust accordingly
	if len(peers) > MaxReceiverCount {
		peers = peers[:MaxReceiverCount]
	}

	return peers, nil
}

func (s *Supply) sendAllRequests(r Request, peers []peer.ID) {
	for _, p := range peers {
		stream, err := s.net.NewRequestStream(p)
		if err != nil {
			continue
		}
		err = stream.WriteRequest(r)
		stream.Close()
		if err != nil {
			continue
		}
	}
}

// GetStoreID returns the StoreID of the store which has the given content
func (s *Supply) GetStoreID(id cid.Cid) (multistore.StoreID, error) {
	rec, err := s.store.GetRecord(id)
	if err != nil {
		return 0, err
	}
	sid, ok := rec.Labels[KStoreID]
	if !ok {
		return 0, fmt.Errorf("storeID not found")
	}
	storeID, err := strconv.ParseUint(sid, 10, 64)
	if err != nil {
		return 0, err
	}
	return multistore.StoreID(storeID), nil
}

// GetStore returns the correct multistore associated with a data CID
func (s *Supply) GetStore(id cid.Cid) (*multistore.Store, error) {
	storeID, err := s.GetStoreID(id)
	if err != nil {
		return nil, err
	}
	store, err := s.ms.Get(storeID)
	if err != nil {
		return nil, err
	}
	return store, nil
}

// RemoveContent removes all content linked to a root CID by completed dropping the store
func (s *Supply) RemoveContent(root cid.Cid) error {
	storeID, err := s.GetStoreID(root)
	if err != nil {
		return err
	}
	err = s.ms.Delete(storeID)
	if err != nil {
		return err
	}
	return s.store.RemoveRecord(root)
}

// ListMiners returns a list of miners based on the regions this supply is part of
// We keep a context as this could also query a remote service or API
func (s *Supply) ListMiners(ctx context.Context) ([]address.Address, error) {
	var strList []string
	for _, r := range s.regions {
		// Global region is already a list of miners in all regions
		if r.Name == "Global" {
			strList = r.StorageMiners
			break
		}
		strList = append(strList, r.StorageMiners...)
	}
	var addrList []address.Address
	for _, s := range strList {
		addr, err := address.NewFromString(s)
		if err != nil {
			return addrList, err
		}
		addrList = append(addrList, addr)
	}
	return addrList, nil
}

// StoreConfigurableTransport defines the methods needed to
// configure a data transfer transport use a unique store for a given request
type StoreConfigurableTransport interface {
	UseStore(datatransfer.ChannelID, ipld.Loader, ipld.Storer) error
}

// TransportConfigurer configurers the graphsync transport to use a custom blockstore per content
func TransportConfigurer(s *Supply) datatransfer.TransportConfigurer {
	return func(channelID datatransfer.ChannelID, voucher datatransfer.Voucher, transport datatransfer.Transport) {
		warn := func(err error) {
			fmt.Println("attempting to configure data store:", err)
		}
		request, ok := voucher.(*Request)
		if !ok {
			return
		}
		gsTransport, ok := transport.(StoreConfigurableTransport)
		if !ok {
			return
		}
		store, err := s.GetStore(request.PayloadCID)
		if err != nil {
			warn(err)
			return
		}
		err = gsTransport.UseStore(channelID, store.Loader, store.Storer)
		if err != nil {
			warn(err)
		}
	}
}

// Validator implements the validation interface for the data transfer manager
// We can authorize peers to retrieve content from us by adding them to the set
type Validator struct {
	mu   sync.Mutex
	auth map[cid.Cid]*peer.Set
}

// Authorize adds a peer to a set giving authorization to pull content without payment
// We assume that this authorizes the peer to pull as many links from the root CID as they can
func (v *Validator) Authorize(k cid.Cid, p peer.ID) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if set, ok := v.auth[k]; ok {
		set.Add(p)
		return
	}
	set := peer.NewSet()
	set.Add(p)
	v.auth[k] = set
}

// ValidatePush returns a stubbed result for a push validation
func (v *Validator) ValidatePush(
	sender peer.ID,
	voucher datatransfer.Voucher,
	baseCid cid.Cid,
	selector ipld.Node) (datatransfer.VoucherResult, error) {
	return nil, fmt.Errorf("no pushed accepted")
}

// ValidatePull returns a stubbed result for a pull validation
func (v *Validator) ValidatePull(
	receiver peer.ID,
	voucher datatransfer.Voucher,
	baseCid cid.Cid,
	selector ipld.Node) (datatransfer.VoucherResult, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	set, ok := v.auth[baseCid]
	if !ok {
		return nil, fmt.Errorf("unknown CID")
	}
	if !set.Contains(receiver) {
		return nil, fmt.Errorf("not authorized")
	}
	return nil, nil
}
