package jwt

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gostack/clock"
)

// Token contains the data structure of a JWT.
type Token struct {
	Type      Type
	Algorithm Algorithm
	KeyID     string
	JWTID     string
	Issuer    string
	Subject   string
	Audience  string
	IssuedAt  time.Time
	Expires   time.Time
	NotBefore time.Time
	Claims    map[string]interface{}
}

// NewToken creates a new Token struct using the default HS256 algorithm.
// IssuedAt is also initialized to the current UTC time.
func NewToken() *Token {
	now := clock.Now().UTC()

	return &Token{
		Type:      JWT,
		Algorithm: HS256,
		IssuedAt:  now,
		NotBefore: now,
		Expires:   now,
		Claims:    make(map[string]interface{}),
	}
}

// DecodeToken attempts to decode a JWT into a Token structure using the given
// secret for verification. This function will only return an error if decoding
// fails or the signature is invalid.
//
// The secret parameter type is variable. All algorithms support string
// and []byte types, but some also have other custom types.
//
// Note: The "alg" field of the token header is completely ignored in order
// to ensure that a token is what the server expects.
func DecodeToken(token string, algorithm Algorithm, secret interface{}) (*Token, error) {
	s := strings.Split(token, ".")
	if len(s) != 3 {
		return nil, ErrInvalidToken
	}

	t := NewToken()

	if err := decodeHeader(t, s[0]); err != nil {
		return nil, err
	}

	if err := decodePayload(t, s[1]); err != nil {
		return nil, err
	}

	if keyLookupCallback != nil {
		if len(t.KeyID) == 0 {
			return nil, ErrNoKeyProvided
		}

		var key interface{}
		algorithm, key = keyLookupCallback(t.KeyID)
		if len(string(algorithm)) == 0 {
			return nil, ErrNonExistantKey
		}
		if key != nil {
			if _, ok := key.(string); !ok || (ok && len(key.(string)) > 0) {
				secret = key
			}
		}
	}

	if algorithm != None {
		tkn := fmt.Sprintf("%s.%s", s[0], s[1])

		pair, ok := supportedAlgorithms[algorithm]
		if !ok {
			return nil, ErrUnsupportedAlgorithm
		}

		if err := pair.Verifier(tkn, s[2], secret); err != nil {
			return nil, err
		}
	} else {
		if secret != nil {
			return nil, ErrNoneAlgorithmWithSecret
		}
	}

	return t, nil
}

// decodeHeader attempts to decode the JWT header.
func decodeHeader(t *Token, s string) error {
	b, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return err
	}

	header := make(map[string]interface{})
	if err := json.Unmarshal(b, &header); err != nil {
		return err
	}

	if v, ok := header["typ"]; ok {
		if _, ok := v.(string); !ok {
			return ErrInvalidToken
		}
		if _, ok := supportedTypes[Type(v.(string))]; !ok {
			return ErrUnsupportedTokenType
		}
		t.Type = Type(v.(string))
	}

	if v, ok := header["alg"]; ok {
		if _, ok := v.(string); !ok {
			return ErrInvalidToken
		}
		t.Algorithm = Algorithm(v.(string))
	}

	if v, ok := header["kid"]; ok {
		if _, ok := v.(string); !ok {
			return ErrInvalidToken
		}
		t.KeyID = v.(string)
	}

	return nil
}

// decodePayload attempts to decode the JWT payload.
func decodePayload(t *Token, s string) error {
	b, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return err
	}

	payload := make(map[string]interface{})
	if err := json.Unmarshal(b, &payload); err != nil {
		return err
	}

	if v, ok := payload["jti"]; ok {
		if _, ok := v.(string); !ok {
			return ErrInvalidToken
		}
		t.JWTID = v.(string)
	}

	if v, ok := payload["iss"]; ok {
		if _, ok := v.(string); !ok {
			return ErrInvalidToken
		}
		t.Issuer = v.(string)
	}
	if v, ok := payload["sub"]; ok {
		if _, ok := v.(string); !ok {
			return ErrInvalidToken
		}
		t.Subject = v.(string)
	}
	if v, ok := payload["aud"]; ok {
		if _, ok := v.(string); !ok {
			return ErrInvalidToken
		}
		t.Audience = v.(string)
	}

	if v, ok := payload["iat"]; ok {
		if _, ok := v.(float64); !ok {
			return ErrInvalidToken
		}
		t.IssuedAt = time.Unix(int64(v.(float64)), 0)
	}
	if v, ok := payload["nbf"]; ok {
		if _, ok := v.(float64); !ok {
			return ErrInvalidToken
		}
		t.NotBefore = time.Unix(int64(v.(float64)), 0)
	}
	if v, ok := payload["exp"]; ok {
		if _, ok := v.(float64); !ok {
			return ErrInvalidToken
		}
		t.Expires = time.Unix(int64(v.(float64)), 0)
	}

	for k, v := range payload {
		if _, ok := reservedClaims[k]; ok {
			continue
		}
		t.Claims[k] = v
	}

	return nil
}

// Sign signs the token with the provided secret and returns the base64 encoded
// string value of the token.
//
// The secret parameter type is variable. All algorithms support string
// and []byte types, but some also have other custom types.
// Refer to the documentation in each encryption package for the options.
func (t Token) Sign(secret interface{}) (string, error) {
	header, err := json.Marshal(t.buildHeader())
	if err != nil {
		return "", err
	}

	claims, err := t.buildClaims()
	if err != nil {
		return "", err
	}

	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}

	tkn := fmt.Sprintf(
		"%s.%s",
		base64.URLEncoding.EncodeToString(header),
		base64.URLEncoding.EncodeToString(payload),
	)

	signature := ""
	if t.Algorithm != None {
		pair, ok := supportedAlgorithms[t.Algorithm]
		if !ok {
			return "", ErrUnsupportedAlgorithm
		}
		signature, err = pair.Signer(tkn, secret)
		if err != nil {
			return "", err
		}
	}

	return fmt.Sprintf(
		"%s.%s",
		tkn,
		signature,
	), nil
}

// Verify attempts to verify the token using the provided issuer, subject and
// audience. If either provided value is left empty, the value is skipped.
// Validity and expiration will also be checked.
func (t Token) Verify(issuer, subject, audience string) error {
	if len(issuer) > 0 && issuer != t.Issuer {
		return ErrInvalidIssuer
	}

	if len(subject) > 0 && subject != t.Subject {
		return ErrInvalidSubject
	}

	if len(audience) > 0 && audience != t.Audience {
		return ErrInvalidAudience
	}

	if !t.Valid() {
		return ErrTokenNotValidYet
	}

	if t.Expired() {
		return ErrTokenExpired
	}

	return nil
}

// Valid checks if the token is valid yet.
func (t Token) Valid() bool {
	return t.NotBefore.Before(clock.Now().UTC())
}

// Expired checks if the token has expired.
func (t Token) Expired() bool {
	if t.IssuedAt.Equal(t.Expires) {
		return false
	}

	return clock.Now().UTC().After(t.Expires)
}

// buildHeader builds a new header map ready for signing.
func (t Token) buildHeader() map[string]interface{} {
	header := make(map[string]interface{})

	header["typ"] = t.Type
	header["alg"] = t.Algorithm
	if len(t.KeyID) > 0 {
		header["kid"] = t.KeyID
	}

	return header
}

// buildClaims builds a new claims map ready for signing.
func (t Token) buildClaims() (map[string]interface{}, error) {
	claims := make(map[string]interface{})

	if len(t.JWTID) > 0 {
		claims["jti"] = t.JWTID
	}
	if len(t.Issuer) > 0 {
		claims["iss"] = t.Issuer
	}
	if len(t.Subject) > 0 {
		claims["sub"] = t.Subject
	}
	if len(t.Audience) > 0 {
		claims["aud"] = t.Audience
	}

	claims["iat"] = t.IssuedAt.Unix()
	if t.NotBefore.Before(t.IssuedAt) {
		claims["nbf"] = t.NotBefore.Unix()
	}
	if t.Expires.After(t.IssuedAt) {
		claims["exp"] = t.Expires.Unix()
	}

	for k, v := range t.Claims {
		if _, ok := reservedClaims[k]; ok {
			return claims, ErrReservedClaim
		}
		claims[k] = v
	}

	return claims, nil
}
