package payments

import (
	"testing"

	"github.com/filecoin-project/go-address"
	tutils "github.com/filecoin-project/specs-actors/v3/support/testing"
	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/sync"
	"github.com/stretchr/testify/require"
)

// This test is the same as lotus making sure we don't break anything
func TestChannelStore(t *testing.T) {
	store := NewStore(sync.MutexWrap(ds.NewMapDatastore()))

	addrs, err := store.ListChannels()
	require.NoError(t, err)
	require.Len(t, addrs, 0)

	ch := tutils.NewIDAddr(t, 100)
	ci := &ChannelInfo{
		Channel: &ch,
		Control: tutils.NewIDAddr(t, 101),
		Target:  tutils.NewIDAddr(t, 102),

		Direction: DirOutbound,
		Vouchers:  []*VoucherInfo{{Voucher: nil, Proof: []byte{}}},
	}

	ch2 := tutils.NewIDAddr(t, 200)
	ci2 := &ChannelInfo{
		Channel: &ch2,
		Control: tutils.NewIDAddr(t, 201),
		Target:  tutils.NewIDAddr(t, 202),

		Direction: DirOutbound,
		Vouchers:  []*VoucherInfo{{Voucher: nil, Proof: []byte{}}},
	}

	// Track the channel
	_, err = store.TrackChannel(ci)
	require.NoError(t, err)

	// Tracking same channel again should error
	_, err = store.TrackChannel(ci)
	require.Error(t, err)

	// Track another channel
	_, err = store.TrackChannel(ci2)
	require.NoError(t, err)

	// List channels should include all channels
	addrs, err = store.ListChannels()
	require.NoError(t, err)
	require.Len(t, addrs, 2)
	t0100, err := address.NewIDAddress(100)
	require.NoError(t, err)
	t0200, err := address.NewIDAddress(200)
	require.NoError(t, err)
	require.Contains(t, addrs, t0100)
	require.Contains(t, addrs, t0200)

	// Request vouchers for channel
	vouchers, err := store.VouchersForPaych(*ci.Channel)
	require.NoError(t, err)
	require.Len(t, vouchers, 1)

	// Requesting voucher for non-existent channel should error
	_, err = store.VouchersForPaych(tutils.NewIDAddr(t, 300))
	require.Equal(t, err, ErrChannelNotTracked)

	// Allocate lane for channel
	lane, err := store.AllocateLane(*ci.Channel)
	require.NoError(t, err)
	require.Equal(t, lane, uint64(0))

	// Allocate next lane for channel
	lane, err = store.AllocateLane(*ci.Channel)
	require.NoError(t, err)
	require.Equal(t, lane, uint64(1))

	// Allocate next lane for non-existent channel should error
	_, err = store.AllocateLane(tutils.NewIDAddr(t, 300))
	require.Equal(t, err, ErrChannelNotTracked)
}
