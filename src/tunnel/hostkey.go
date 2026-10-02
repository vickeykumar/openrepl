package tunnel

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"io/ioutil"
	"os"

	"golang.org/x/crypto/ssh"
)

// LoadOrCreateHostKey reads the gateway's tunnel host key from path, creating
// an ed25519 key there (mode 0600) if the file does not exist.
func LoadOrCreateHostKey(path string) (ssh.Signer, error) {
	data, err := ioutil.ReadFile(path)
	if os.IsNotExist(err) {
		_, priv, gerr := ed25519.GenerateKey(rand.Reader)
		if gerr != nil {
			return nil, gerr
		}
		block, merr := ssh.MarshalPrivateKey(priv, "openrepl tunnel host key")
		if merr != nil {
			return nil, merr
		}
		data = pem.EncodeToMemory(block)
		if werr := ioutil.WriteFile(path, data, 0600); werr != nil {
			return nil, werr
		}
	} else if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(data)
}

// NewEphemeralHostKey returns a host key that is not stored anywhere.
func NewEphemeralHostKey() (ssh.Signer, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return ssh.NewSignerFromKey(priv)
}

// Fingerprint is the SHA256 fingerprint workers pin with --worker-hostkey.
func Fingerprint(signer ssh.Signer) string {
	return ssh.FingerprintSHA256(signer.PublicKey())
}
