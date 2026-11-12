package sshkeys

import (
	"strings"
	"testing"
)

func wantRefusal(t *testing.T, content, want string) {
	t.Helper()
	_, _, err := parsePublicKey(content)

	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v, want it to contain %q", err, want)
	}
}

func TestParsePublicKeyKeepsTheKeyLineAndItsComment(t *testing.T) {
	publicKey, comment, err := parsePublicKey("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI me@laptop\n")

	if err != nil || publicKey != "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI me@laptop" || comment != "me@laptop" {
		t.Errorf("got %q, %q, %v", publicKey, comment, err)
	}
}

func TestParsePublicKeyAcceptsAKeyWithoutAComment(t *testing.T) {
	_, comment, err := parsePublicKey("ecdsa-sha2-nistp256 AAAAE2VjZHNh")

	if err != nil || comment != "" {
		t.Errorf("got comment %q, %v; want none", comment, err)
	}
}

func TestParsePublicKeyRefusesAPrivateKey(t *testing.T) {
	wantRefusal(t, "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----", "That is a private key.")
}

func TestParsePublicKeyRefusesAnythingElse(t *testing.T) {
	wantRefusal(t, "hello world", "That is not an OpenSSH public key.")
}
