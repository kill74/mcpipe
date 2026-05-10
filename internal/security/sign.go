package security

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
)

const (
	privateKeyType = "MCPIPE ED25519 PRIVATE KEY"
	publicKeyType  = "MCPIPE ED25519 PUBLIC KEY"
)

func GenerateKeypair(privatePath, publicPath string) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.WriteFile(privatePath, pem.EncodeToMemory(&pem.Block{Type: privateKeyType, Bytes: priv}), 0600); err != nil {
		return err
	}
	return os.WriteFile(publicPath, pem.EncodeToMemory(&pem.Block{Type: publicKeyType, Bytes: pub}), 0644)
}

func SignFile(path, privateKeyPath, sigPath string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	priv, err := readPrivateKey(privateKeyPath)
	if err != nil {
		return err
	}
	sig := ed25519.Sign(priv, data)
	return os.WriteFile(sigPath, []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0644)
}

func VerifySignature(path, publicKeyPath, sigPath string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	pub, err := readPublicKey(publicKeyPath)
	if err != nil {
		return err
	}
	sigText, err := os.ReadFile(sigPath)
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(stringTrimSpace(sigText))
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, data, sig) {
		return errors.New("signature verification failed")
	}
	return nil
}

func readPrivateKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != privateKeyType {
		return nil, fmt.Errorf("%s is not an mcpipe private key", path)
	}
	if len(block.Bytes) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%s has invalid private key length", path)
	}
	return ed25519.PrivateKey(block.Bytes), nil
}

func readPublicKey(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != publicKeyType {
		return nil, fmt.Errorf("%s is not an mcpipe public key", path)
	}
	if len(block.Bytes) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%s has invalid public key length", path)
	}
	return ed25519.PublicKey(block.Bytes), nil
}

func stringTrimSpace(data []byte) string {
	out := string(data)
	for len(out) > 0 && (out[len(out)-1] == '\n' || out[len(out)-1] == '\r' || out[len(out)-1] == ' ' || out[len(out)-1] == '\t') {
		out = out[:len(out)-1]
	}
	for len(out) > 0 && (out[0] == '\n' || out[0] == '\r' || out[0] == ' ' || out[0] == '\t') {
		out = out[1:]
	}
	return out
}
