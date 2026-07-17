package cryptox

import "testing"

func TestRoundTrip(t *testing.T) {
	c, err := New("test-key")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := c.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if string(encrypted) == "secret" {
		t.Fatal("plaintext was not encrypted")
	}
	plain, err := c.Decrypt(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "secret" {
		t.Fatalf("got %q", plain)
	}
}
