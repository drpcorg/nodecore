package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/drpcorg/nodecore/internal/quorum"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const values = `
providerConfig:
  providers:
    - name: zeta
      cluster:
        US-East:
          nodes:
            - host: a
              public_key: /rsa/zeta-a.pem
            - host: b
              public_key: /rsa/zeta-b.pem
    - name: alpha
      cluster:
        EU-West:
          nodes:
            - host: c
              public_key: /rsa/zeta-b.pem
`

func secret(t *testing.T, blockType string, der []byte) string {
	t.Helper()
	return base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}))
}

// The node index inside its region picks the key, a PKCS#1 key is stored as
// PKIX, entries come sorted, and nodecore's registry loads the result.
func TestBuildRender(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	ecDER, err := x509.MarshalPKIXPublicKey(&ecKey.PublicKey)
	require.NoError(t, err)

	keys := "zeta-a.pem: " + secret(t, "PUBLIC KEY", ecDER) + "\n" +
		"zeta-b.pem: " + secret(t, "RSA PUBLIC KEY", x509.MarshalPKCS1PublicKey(&rsaKey.PublicKey)) + "\n"

	entries, err := build([]byte(values), []byte(keys))
	require.NoError(t, err)
	out, err := render(entries)
	require.NoError(t, err)

	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.ProviderID
	}
	assert.Equal(t, []string{"alpha@EU-West#0", "zeta@US-East#0", "zeta@US-East#1"}, ids)

	reg, err := quorum.LoadRegistry(out)
	require.NoError(t, err)
	first, ok := reg.Lookup("zeta@US-East#0")
	require.True(t, ok)
	assert.True(t, ecKey.PublicKey.Equal(first))
	second, ok := reg.Lookup("zeta@US-East#1")
	require.True(t, ok)
	assert.True(t, rsaKey.PublicKey.Equal(second))
}

func TestBuildMissingKey(t *testing.T) {
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKIXPublicKey(&ecKey.PublicKey)
	require.NoError(t, err)

	_, err = build([]byte(values), []byte("zeta-a.pem: "+secret(t, "PUBLIC KEY", der)+"\n"))
	require.Error(t, err)
	msg := err.Error()
	assert.True(t, strings.Contains(msg, "zeta@US-East#1") && strings.Contains(msg, "alpha@EU-West#0"), msg)
}
