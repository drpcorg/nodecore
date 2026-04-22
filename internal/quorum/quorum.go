// Package quorum verifies signatures returned by upstream providers when a
// client requests quorum reads (via quorum=N&quorum_required=n query params).
//
// Each participating upstream returns a header in the strict form:
//
//	QR<N>-id-<request-id>: <provider-id>(<upstream-id>)_nonce_<nonce>_sig_<hex-signature>
//
// All four components are required. The <provider-id> (before the
// parentheses) is used to look up the public key in provider_keys.yaml.
// The <upstream-id> (inside the parentheses) is the dshackle `source` and
// is what the signature is actually computed over. Any header whose value
// does not match this exact shape is rejected as malformed.
//
// The signed message is produced with one of:
//   - SHA256withECDSA over NIST P-256 (signature is DER-encoded ASN.1), or
//   - SHA256withRSA / PKCS#1 v1.5 (signature is a fixed-size integer block)
//
// over the string "DSHACKLESIG/<nonce>/<upstream-id>/<hex(sha256(result))>".
// The concrete algorithm is inferred from the type of the public key loaded
// from provider_keys.yaml.
package quorum

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/drpcorg/nodecore/internal/protocol"
	"gopkg.in/yaml.v3"
)

const (
	headerValueNonceSep = "_nonce_"
	headerValueSigSep   = "_sig_"
	msgPrefix           = "DSHACKLESIG"
	msgSeparator        = "/"
)

var qrHeaderNameRe = regexp.MustCompile(`(?i)^QR(\d+)-id-(.+)$`)

type Signature struct {
	Index      int
	RequestID  string
	ProviderID string // key in provider_keys.yaml (header prefix before the optional "(...)" )
	UpstreamID string // dshackle `source` used for signing (content inside parens, falls back to ProviderID)
	Nonce      uint64
	Raw        []byte
}

type Registry struct {
	keys map[string]crypto.PublicKey
}

var (
	ErrMissingSignatures      = errors.New("quorum: no QR signature headers present")
	ErrInsufficientSignatures = errors.New("quorum: not enough signatures returned")
	ErrUnknownProvider        = errors.New("quorum: no public key for provider")
	ErrInvalidSignature       = errors.New("quorum: signature verification failed")
	ErrMalformedHeader        = errors.New("quorum: malformed QR header")
	ErrUnexpectedRequestID    = errors.New("quorum: QR header request id does not match request")
	ErrNotSupported           = errors.New("quorum: quorum is not supported for this request")
)

// UnknownProviderError is returned when a QR header references a provider
// that is not present in the local keys registry.
type UnknownProviderError struct {
	ProviderID string
	Index      int
}

func (e *UnknownProviderError) Error() string {
	if e.ProviderID == "" {
		return ErrUnknownProvider.Error()
	}
	return fmt.Sprintf("%s: %s", ErrUnknownProvider.Error(), e.ProviderID)
}

func (e *UnknownProviderError) Is(target error) bool {
	return target == ErrUnknownProvider
}

// InvalidSignatureError is returned when a QR signature does not verify
// against the provider's public key and the response body.
type InvalidSignatureError struct {
	ProviderID string
	Index      int
	Cause      error
}

func (e *InvalidSignatureError) Error() string {
	if e.ProviderID == "" {
		return ErrInvalidSignature.Error()
	}
	return fmt.Sprintf("QR%d (%s): %s", e.Index, e.ProviderID, ErrInvalidSignature.Error())
}

func (e *InvalidSignatureError) Is(target error) bool {
	return target == ErrInvalidSignature
}

func (e *InvalidSignatureError) Unwrap() error { return e.Cause }

// InsufficientSignaturesError is returned when the upstream returned fewer
// valid signatures than the client required via `quorum_required`.
type InsufficientSignaturesError struct {
	Got      int
	Required int
}

func (e *InsufficientSignaturesError) Error() string {
	return fmt.Sprintf("%s: got %d, need %d", ErrInsufficientSignatures.Error(), e.Got, e.Required)
}

func (e *InsufficientSignaturesError) Is(target error) bool {
	return target == ErrInsufficientSignatures
}

// MalformedHeaderError is returned when a QR* header does not match the
// strict `<providerID>(<upstreamID>)_nonce_<n>_sig_<hex>` shape.
type MalformedHeaderError struct {
	HeaderName string
	Cause      error
}

func (e *MalformedHeaderError) Error() string {
	if e.HeaderName == "" {
		if e.Cause == nil {
			return ErrMalformedHeader.Error()
		}
		return fmt.Sprintf("%s: %s", ErrMalformedHeader.Error(), e.Cause.Error())
	}
	if e.Cause == nil {
		return fmt.Sprintf("%s: header %q", ErrMalformedHeader.Error(), e.HeaderName)
	}
	return fmt.Sprintf("%s: header %q: %s", ErrMalformedHeader.Error(), e.HeaderName, e.Cause.Error())
}

func (e *MalformedHeaderError) Is(target error) bool {
	return target == ErrMalformedHeader
}

func (e *MalformedHeaderError) Unwrap() error { return e.Cause }

// UnexpectedRequestIDError is returned when the request-id embedded in the
// QR header name does not match the actual JSON-RPC request id the gateway
// sent. This prevents an upstream from replaying signatures across requests.
type UnexpectedRequestIDError struct {
	Expected string
	Got      string
	Index    int
}

func (e *UnexpectedRequestIDError) Error() string {
	return fmt.Sprintf("%s: QR%d expected %q, got %q", ErrUnexpectedRequestID.Error(), e.Index, e.Expected, e.Got)
}

func (e *UnexpectedRequestIDError) Is(target error) bool {
	return target == ErrUnexpectedRequestID
}

// NotSupportedError is returned when the current request shape cannot be
// served with quorum reads (no DRPC upstreams, sticky-send method, response
// is a stream, etc.). Propagated as-is to the client.
type NotSupportedError struct {
	Reason string
}

func (e *NotSupportedError) Error() string {
	if e.Reason == "" {
		return ErrNotSupported.Error()
	}
	return fmt.Sprintf("%s: %s", ErrNotSupported.Error(), e.Reason)
}

func (e *NotSupportedError) Is(target error) bool {
	return target == ErrNotSupported
}

//go:embed provider_keys.yaml
var embeddedProviderKeys []byte

type providerKeyEntry struct {
	ProviderID string `yaml:"provider_id"`
	PublicKey  string `yaml:"public_key"`
}

type providerKeysFile struct {
	Keys []providerKeyEntry `yaml:"keys"`
}

func DefaultRegistry() (*Registry, error) {
	return LoadRegistry(embeddedProviderKeys)
}

func LoadRegistry(data []byte) (*Registry, error) {
	var file providerKeysFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("quorum: parse provider keys: %w", err)
	}
	keys := make(map[string]crypto.PublicKey, len(file.Keys))
	for i, e := range file.Keys {
		if strings.TrimSpace(e.ProviderID) == "" {
			return nil, fmt.Errorf("quorum: entry #%d has empty provider_id", i)
		}
		if _, exists := keys[e.ProviderID]; exists {
			return nil, fmt.Errorf("quorum: duplicate provider_id %q", e.ProviderID)
		}
		pub, err := parsePublicKey([]byte(e.PublicKey))
		if err != nil {
			return nil, fmt.Errorf("quorum: provider %q: %w", e.ProviderID, err)
		}
		keys[e.ProviderID] = pub
	}
	return &Registry{keys: keys}, nil
}

func NewRegistry(keys map[string]crypto.PublicKey) *Registry {
	cp := make(map[string]crypto.PublicKey, len(keys))
	for k, v := range keys {
		cp[k] = v
	}
	return &Registry{keys: cp}
}

func (r *Registry) Len() int {
	if r == nil {
		return 0
	}
	return len(r.keys)
}

// Lookup resolves a public key by provider_id taken verbatim from the QR
// header. Provider ids are fully-qualified as "<name>@<region>#<idx>", where
// <idx> is the 0-based position of the node inside its region in the
// provider-config. provider_keys.yaml is keyed in the exact same shape.
func (r *Registry) Lookup(providerID string) (crypto.PublicKey, bool) {
	if r == nil {
		return nil, false
	}
	pub, ok := r.keys[providerID]
	return pub, ok
}

// VerifyHeaders requires `result` to be the exact raw JSON-RPC "result" bytes
// the provider signed. `minRequired` is the client-supplied `quorum_required=`
// value; fewer valid signatures than this is rejected. When `expectedRequestID`
// is non-empty it is cross-checked against the id encoded in every QR header
// to reject cross-request signature replays. Returns nil only when every QR
// header is present, matches the expected request id, and verifies against
// the provider's public key.
func (r *Registry) VerifyHeaders(headers http.Header, result []byte, expectedRequestID string, minRequired int) error {
	sigs, err := ExtractSignatures(headers)
	if err != nil {
		return err
	}
	if len(sigs) == 0 {
		return ErrMissingSignatures
	}
	if minRequired > 0 && len(sigs) < minRequired {
		return &InsufficientSignaturesError{Got: len(sigs), Required: minRequired}
	}
	for _, s := range sigs {
		// net/http canonicalises header names (`QR0-id-abc` → `Qr0-Id-Abc`), so
		// the request id token is compared case-insensitively. This is safe in
		// practice because JSON-RPC request ids are typically numeric.
		if expectedRequestID != "" && !strings.EqualFold(s.RequestID, expectedRequestID) {
			return &UnexpectedRequestIDError{
				Expected: expectedRequestID,
				Got:      s.RequestID,
				Index:    s.Index,
			}
		}
		if err := r.verifySignature(s, result); err != nil {
			return err
		}
	}
	return nil
}

// Verify is a low-level helper mainly useful in tests: callers usually
// go through VerifyHeaders which handles header parsing and the source
// vs provider-id split. `upstreamID` is the string dshackle fed to its
// signer as `source` — i.e. what's in parens in the QR header prefix.
func (r *Registry) Verify(providerID, upstreamID string, nonce uint64, signature, message []byte) error {
	pub, ok := r.Lookup(providerID)
	if !ok {
		return &UnknownProviderError{ProviderID: providerID}
	}
	wrapped := WrapMessage(nonce, upstreamID, message)
	digest := sha256.Sum256(wrapped)
	if err := verifyDigest(pub, digest[:], signature); err != nil {
		return &InvalidSignatureError{ProviderID: providerID, Cause: err}
	}
	return nil
}

func (r *Registry) verifySignature(s Signature, result []byte) error {
	pub, ok := r.Lookup(s.ProviderID)
	if !ok {
		return &UnknownProviderError{ProviderID: s.ProviderID, Index: s.Index}
	}
	wrapped := WrapMessage(s.Nonce, s.UpstreamID, result)
	digest := sha256.Sum256(wrapped)
	if err := verifyDigest(pub, digest[:], s.Raw); err != nil {
		return &InvalidSignatureError{ProviderID: s.ProviderID, Index: s.Index, Cause: err}
	}
	return nil
}

// verifyDigest dispatches to the right verification primitive based on the
// type of the registered public key. Only SHA-256 digests are supported
// (matches dshackle's SHA256withECDSA / SHA256withRSA schemes). Callers
// wrap the raw error into InvalidSignatureError to attach provider context.
func verifyDigest(pub crypto.PublicKey, digest, signature []byte) error {
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		if !ecdsa.VerifyASN1(k, digest, signature) {
			return errors.New("ecdsa: signature does not verify")
		}
		return nil
	case *rsa.PublicKey:
		if err := rsa.VerifyPKCS1v15(k, crypto.SHA256, digest, signature); err != nil {
			return fmt.Errorf("rsa: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported public key type %T", pub)
	}
}

// WrapMessage reproduces dshackle's ResponseSigner.wrapMessage:
// "DSHACKLESIG/<nonce>/<source>/<hex(sha256(message))>". `source` here is
// what dshackle passed as `source` — the upstream id (what's in parens in
// the QR header prefix).
func WrapMessage(nonce uint64, source string, message []byte) []byte {
	digest := sha256.Sum256(message)
	hexDigest := hex.EncodeToString(digest[:])

	var b strings.Builder
	b.Grow(len(msgPrefix) + 1 + 20 + 1 + len(source) + 1 + len(hexDigest))
	b.WriteString(msgPrefix)
	b.WriteString(msgSeparator)
	b.WriteString(strconv.FormatUint(nonce, 10))
	b.WriteString(msgSeparator)
	b.WriteString(source)
	b.WriteString(msgSeparator)
	b.WriteString(hexDigest)
	return []byte(b.String())
}

func ExtractSignatures(headers http.Header) ([]Signature, error) {
	if len(headers) == 0 {
		return nil, nil
	}
	out := make([]Signature, 0, 4)
	for name, values := range headers {
		m := qrHeaderNameRe.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		index, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, &MalformedHeaderError{HeaderName: name, Cause: fmt.Errorf("bad index: %w", err)}
		}
		reqID := m[2]
		for _, value := range values {
			prefix, nonce, sig, err := parseHeaderValue(value)
			if err != nil {
				return nil, &MalformedHeaderError{HeaderName: name, Cause: err}
			}
			providerID, upstreamID, err := splitPrefix(prefix)
			if err != nil {
				return nil, &MalformedHeaderError{HeaderName: name, Cause: err}
			}
			out = append(out, Signature{
				Index:      index,
				RequestID:  reqID,
				ProviderID: providerID,
				UpstreamID: upstreamID,
				Nonce:      nonce,
				Raw:        sig,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out, nil
}

// splitPrefix parses the header prefix "<providerID>(<upstreamID>)" and
// returns its two parts. Both the provider id (for public-key lookup) and
// the upstream id (the dshackle `source` that got signed) are mandatory —
// any other shape is rejected.
func splitPrefix(prefix string) (providerID, upstreamID string, err error) {
	if !strings.HasSuffix(prefix, ")") {
		return "", "", fmt.Errorf("prefix %q must end with \")\"", prefix)
	}
	open := strings.LastIndexByte(prefix[:len(prefix)-1], '(')
	if open < 0 {
		return "", "", fmt.Errorf("prefix %q is missing \"(\"", prefix)
	}
	providerID = prefix[:open]
	upstreamID = prefix[open+1 : len(prefix)-1]
	if providerID == "" {
		return "", "", errors.New("empty provider id")
	}
	if upstreamID == "" {
		return "", "", errors.New("empty upstream id")
	}
	return providerID, upstreamID, nil
}

func parseHeaderValue(value string) (string, uint64, []byte, error) {
	nonceIdx := strings.Index(value, headerValueNonceSep)
	if nonceIdx < 0 {
		return "", 0, nil, fmt.Errorf("missing %q separator", headerValueNonceSep)
	}
	prefix := value[:nonceIdx]
	if prefix == "" {
		return "", 0, nil, errors.New("empty prefix")
	}
	rest := value[nonceIdx+len(headerValueNonceSep):]

	sigIdx := strings.Index(rest, headerValueSigSep)
	if sigIdx < 0 {
		return "", 0, nil, fmt.Errorf("missing %q separator", headerValueSigSep)
	}
	nonceStr := rest[:sigIdx]
	nonce, err := strconv.ParseUint(nonceStr, 10, 64)
	if err != nil {
		return "", 0, nil, fmt.Errorf("invalid nonce %q: %w", nonceStr, err)
	}

	sigHex := rest[sigIdx+len(headerValueSigSep):]
	sigHex = strings.TrimPrefix(sigHex, "0x")
	sigHex = strings.TrimPrefix(sigHex, "0X")
	if sigHex == "" {
		return "", 0, nil, errors.New("empty signature")
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return "", 0, nil, fmt.Errorf("invalid hex signature: %w", err)
	}
	return prefix, nonce, sig, nil
}

func ToResponseError(err error) *protocol.ResponseError {
	if err == nil {
		return nil
	}
	var unknown *UnknownProviderError
	if errors.As(err, &unknown) {
		return protocol.QuorumUnknownProviderError(unknown.ProviderID)
	}
	var invalid *InvalidSignatureError
	if errors.As(err, &invalid) {
		return protocol.QuorumInvalidSignatureError(invalid.ProviderID)
	}
	var insufficient *InsufficientSignaturesError
	if errors.As(err, &insufficient) {
		return protocol.QuorumInsufficientSignaturesError(insufficient.Got, insufficient.Required)
	}
	var malformed *MalformedHeaderError
	if errors.As(err, &malformed) {
		return protocol.QuorumMalformedHeaderError(err)
	}
	var unexpected *UnexpectedRequestIDError
	if errors.As(err, &unexpected) {
		return protocol.QuorumUnexpectedRequestIDError(unexpected.Expected, unexpected.Got)
	}
	var notSupported *NotSupportedError
	if errors.As(err, &notSupported) {
		return protocol.QuorumNotSupportedError(notSupported.Reason)
	}
	if errors.Is(err, ErrMissingSignatures) {
		return protocol.QuorumMissingSignaturesError()
	}
	return protocol.QuorumVerificationError(err)
}

// parsePublicKey accepts base64-encoded X.509 SubjectPublicKeyInfo (the
// middle of a PEM block). Whitespace is ignored. Both ECDSA and RSA keys
// are accepted; the concrete scheme used for verification is picked based
// on the decoded key type.
func parsePublicKey(raw []byte) (crypto.PublicKey, error) {
	cleaned := stripWhitespace(string(raw))
	if cleaned == "" {
		return nil, errors.New("empty public key")
	}
	der, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil {
		return nil, fmt.Errorf("decode base64 public key: %w", err)
	}
	pubAny, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("parse X.509 public key: %w", err)
	}
	switch pubAny.(type) {
	case *ecdsa.PublicKey, *rsa.PublicKey:
		return pubAny, nil
	default:
		return nil, fmt.Errorf("unsupported public key type %T (want ECDSA or RSA)", pubAny)
	}
}

func stripWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case ' ', '\t', '\r', '\n':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
