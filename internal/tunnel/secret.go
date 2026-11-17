// Package tunnel carries B's browser traffic to A's local proxy through the
// signaling server's relay. The relay only sees TLS 1.3 ciphertext: both ends
// derive their keys from an invite secret that is shared out-of-band and never
// sent to the server.
package tunnel

import (
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base32"
	"errors"
	"math/big"
	"strings"
	"time"
)

const secretChars = 26 // base32 of 16 bytes, no padding

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// Secret is the per-room invite secret (128 bits).
type Secret [16]byte

func NewSecret() (Secret, error) {
	var s Secret
	_, err := rand.Read(s[:])
	return s, err
}

func (s Secret) String() string { return b32.EncodeToString(s[:]) }

// FormatInvite returns the code A copies to B: ROOM-KEY-SECRET, plus
// "@relay-host" when the room lives on A's private relay, so B finds it
// without any setup. relayURL "" means the default public relay.
func FormatInvite(roomKey string, s Secret, relayURL string) string {
	inv := roomKey + "-" + s.String()
	if relayURL != "" {
		inv += "@" + strings.TrimPrefix(relayURL, "https://")
	}
	return inv
}

// ParseInvite accepts the invite with or without dashes/spaces. relayURL is
// "" for the public relay, otherwise the (unvalidated) private relay URL.
func ParseInvite(in string) (roomKey string, s Secret, relayURL string, err error) {
	code, relay, hasRelay := strings.Cut(strings.TrimSpace(in), "@")
	if hasRelay {
		relay = strings.TrimSpace(relay)
		if relay == "" {
			return "", s, "", errors.New("invite code is incomplete: the relay address after @ is missing")
		}
		if !strings.Contains(relay, "://") {
			relay = "https://" + relay
		}
	}
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if (r >= 'A' && r <= 'Z') || (r >= '2' && r <= '7') {
			b.WriteRune(r)
		}
	}
	clean := b.String()
	if len(clean) != 8+secretChars {
		return "", s, "", errors.New("invite code is incomplete: paste the full code from the host")
	}
	raw, err := b32.DecodeString(clean[8:])
	if err != nil || len(raw) != len(s) {
		return "", s, "", errors.New("invite code is invalid")
	}
	copy(s[:], raw)
	return clean[:4] + "-" + clean[4:8], s, relay, nil
}

func (s Secret) derive(label string) []byte {
	k, err := hkdf.Key(sha256.New, s[:], nil, "temproom v2 "+label, 32)
	if err != nil {
		panic(err) // only fails for invalid lengths
	}
	return k
}

func (s Secret) hostKey() ed25519.PrivateKey { return ed25519.NewKeyFromSeed(s.derive("host key")) }

// serverTLS is used by A. Its certificate key is derived from the secret, so B
// can pin it without any certificate authority.
func (s Secret) serverTLS() (*tls.Config, error) {
	priv := s.hostKey()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "temproom"},
		DNSNames:     []string{"temproom"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(72 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, priv.Public(), priv)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: priv}},
	}, nil
}

var errWrongHost = errors.New("tunnel peer is not the room host")

// clientTLS is used by B. TLS 1.3 always checks the handshake signature against
// the leaf key, so pinning that key authenticates A.
func (s Secret) clientTLS() *tls.Config {
	want := s.hostKey().Public().(ed25519.PublicKey)
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		ServerName:         "temproom",
		InsecureSkipVerify: true, // replaced by the pinned-key check below
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errWrongHost
			}
			cert, err := x509.ParseCertificate(raw[0])
			if err != nil {
				return errWrongHost
			}
			if got, ok := cert.PublicKey.(ed25519.PublicKey); !ok || !got.Equal(want) {
				return errWrongHost
			}
			return nil
		},
	}
}

// proof binds knowledge of the secret to this TLS session, so A only serves
// the operator who holds the invite (not anyone who sniffed a token).
func (s Secret) proof(cs tls.ConnectionState) ([]byte, error) {
	ekm, err := cs.ExportKeyingMaterial("EXPORTER-temproom-operator", nil, 32)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, s.derive("operator proof"))
	mac.Write(ekm)
	return mac.Sum(nil), nil
}
