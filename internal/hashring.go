package accesscontroller

import (
	"context"
	"hash/crc32"
	"sync"

	"github.com/buraksezer/consistent"
)

type ctxKey int

var hashringChecksumKey ctxKey

type HashringMember interface {
	String() string
}

// Hashring defines an interface to manage a consistent hash ring.
type Hashring interface {

	// Add adds a new member to the hash ring.
	Add(member HashringMember)

	// Remove removes a member from the hash ring.
	Remove(member HashringMember)

	// LocateKey finds the home member for a given key.
	LocateKey(key []byte) string

	// Checksum computes the crc32 checksum of the Hashring.
	//
	// This can be used to compare the relative state of two
	// hash rings on remote servers. If the checksum is the
	// same, then the two members can trust their memberlist
	// is identical. If not, then at some point in the future
	// the cluster memberlist should converge and then the
	// checksums will be identical.
	Checksum() uint32
}

// NewContext returns a new Context that carries the hash ring checksum.
func NewContext(ctx context.Context, hashringChecksum uint32) context.Context {
	return context.WithValue(ctx, hashringChecksumKey, hashringChecksum)
}

func FromContext(ctx context.Context) (uint32, bool) {
	checksum, ok := ctx.Value(hashringChecksumKey).(uint32)
	return checksum, ok
}

type ConsistentHashring struct {
	rw   sync.RWMutex
	Ring *consistent.Consistent
}

func (ch *ConsistentHashring) Add(member HashringMember) {
	defer ch.rw.Unlock()
	ch.rw.Lock()
	ch.Ring.Add(consistent.Member(member))
}

func (ch *ConsistentHashring) Remove(member HashringMember) {
	defer ch.rw.Unlock()
	ch.rw.Lock()
	ch.Ring.Remove(member.String())
}

func (ch *ConsistentHashring) LocateKey(key []byte) string {
	defer ch.rw.RUnlock()
	ch.rw.RLock()
	return ch.Ring.LocateKey(key).String()
}

func (ch *ConsistentHashring) Checksum() uint32 {
	defer ch.rw.RUnlock()
	ch.rw.RLock()

	members := ch.Ring.GetMembers()

	bytes := []byte{}
	for _, member := range members {
		bytes = append(bytes, []byte(member.String())...)
	}

	return crc32.ChecksumIEEE(bytes)
}
