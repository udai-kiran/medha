package state

import "crypto/rand"

// newID generates a random prefixed ID, e.g. newID("msg") → "msg-a3f1c2b4d5e6a7b8".
func newID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	const hex = "0123456789abcdef"
	out := make([]byte, len(prefix)+1+16)
	copy(out, prefix)
	out[len(prefix)] = '-'
	for i := 0; i < 8; i++ {
		out[len(prefix)+1+i*2] = hex[b[i]>>4]
		out[len(prefix)+1+i*2+1] = hex[b[i]&0xf]
	}
	return string(out)
}
