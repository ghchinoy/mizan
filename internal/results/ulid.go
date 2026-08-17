package results

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sync"
	"time"
)

// crockford is Crockford's base32 alphabet (no I, L, O, U), the ULID encoding.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// ulidState guards monotonic ULID generation for concurrent single-process
// writes: within the same millisecond the 80-bit entropy is incremented rather
// than re-randomized, so IDs generated back-to-back stay strictly ordered (and
// unique). This is the in-repo, cgo-free, dependency-free ULID the design
// mandates (§2 "no second driver / dependency-conscious").
var (
	ulidMu       sync.Mutex
	ulidLastMS   uint64
	ulidLastRand [10]byte
)

// NewULID returns a new 26-character Crockford-base32 ULID: a 48-bit millisecond
// timestamp followed by 80 bits of randomness. ULIDs are lexicographically
// sortable by time, so string ordering matches creation ordering — which is what
// ListChangedSince and "order by run_at desc" rely on.
func NewULID() (string, error) {
	ms := uint64(time.Now().UTC().UnixMilli()) & 0xFFFFFFFFFFFF // 48 bits

	ulidMu.Lock()
	defer ulidMu.Unlock()

	var entropy [10]byte
	switch {
	case ms > ulidLastMS:
		if _, err := rand.Read(entropy[:]); err != nil {
			return "", fmt.Errorf("results: ulid entropy: %w", err)
		}
	default:
		// Same millisecond, or a clock that moved backward: keep the last (>=)
		// timestamp and increment the entropy so the new ULID sorts strictly
		// after the previous one.
		ms = ulidLastMS
		entropy = ulidLastRand
		if !incrEntropy(&entropy) {
			// Entropy overflowed within one ms (astronomically unlikely): bump
			// the timestamp and re-randomize.
			ms = ulidLastMS + 1
			if _, err := rand.Read(entropy[:]); err != nil {
				return "", fmt.Errorf("results: ulid entropy: %w", err)
			}
		}
	}
	ulidLastMS = ms
	ulidLastRand = entropy

	return encodeULID(ms, entropy), nil
}

// incrEntropy increments a 10-byte big-endian counter in place, returning false
// on overflow (all bytes wrapped to zero).
func incrEntropy(e *[10]byte) bool {
	for i := len(e) - 1; i >= 0; i-- {
		e[i]++
		if e[i] != 0 {
			return true
		}
	}
	return false
}

// encodeULID renders a 48-bit timestamp + 80-bit entropy as the canonical
// 26-char Crockford-base32 ULID string.
func encodeULID(ms uint64, entropy [10]byte) string {
	var id [16]byte
	// Pack the 48-bit millisecond timestamp as the high 6 bytes (big-endian) and
	// the 80-bit entropy as the low 10 bytes. Writing via PutUint64 and copying
	// the low 6 bytes avoids explicit uint64->byte truncations (gosec G115).
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], ms)
	copy(id[0:6], ts[2:8])
	copy(id[6:], entropy[:])

	var dst [26]byte
	dst[0] = crockford[(id[0]&224)>>5]
	dst[1] = crockford[id[0]&31]
	dst[2] = crockford[(id[1]&248)>>3]
	dst[3] = crockford[((id[1]&7)<<2)|((id[2]&192)>>6)]
	dst[4] = crockford[(id[2]&62)>>1]
	dst[5] = crockford[((id[2]&1)<<4)|((id[3]&240)>>4)]
	dst[6] = crockford[((id[3]&15)<<1)|((id[4]&128)>>7)]
	dst[7] = crockford[(id[4]&124)>>2]
	dst[8] = crockford[((id[4]&3)<<3)|((id[5]&224)>>5)]
	dst[9] = crockford[id[5]&31]
	dst[10] = crockford[(id[6]&248)>>3]
	dst[11] = crockford[((id[6]&7)<<2)|((id[7]&192)>>6)]
	dst[12] = crockford[(id[7]&62)>>1]
	dst[13] = crockford[((id[7]&1)<<4)|((id[8]&240)>>4)]
	dst[14] = crockford[((id[8]&15)<<1)|((id[9]&128)>>7)]
	dst[15] = crockford[(id[9]&124)>>2]
	dst[16] = crockford[((id[9]&3)<<3)|((id[10]&224)>>5)]
	dst[17] = crockford[id[10]&31]
	dst[18] = crockford[(id[11]&248)>>3]
	dst[19] = crockford[((id[11]&7)<<2)|((id[12]&192)>>6)]
	dst[20] = crockford[(id[12]&62)>>1]
	dst[21] = crockford[((id[12]&1)<<4)|((id[13]&240)>>4)]
	dst[22] = crockford[((id[13]&15)<<1)|((id[14]&128)>>7)]
	dst[23] = crockford[(id[14]&124)>>2]
	dst[24] = crockford[((id[14]&3)<<3)|((id[15]&224)>>5)]
	dst[25] = crockford[id[15]&31]
	return string(dst[:])
}
