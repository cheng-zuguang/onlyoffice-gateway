package jwt

import (
	"errors"
	"fmt"

	golangjwt "github.com/golang-jwt/jwt/v5"
	"github.com/zenmind/onlyoffice-gateway/internal/config"
)

var (
	ErrNoToken       = errors.New("missing authorization header")
	ErrInvalidToken  = errors.New("invalid token")
	ErrServiceNotFound = errors.New("service not found")
)

// VerifyServiceJWT validates a JWT signed by the service's private key
// against the corresponding public key in the Gateway config.
// Returns the parsed claims on success.
func VerifyServiceJWT(cfg *config.Config, tokenString string) (golangjwt.MapClaims, error) {
	parser := golangjwt.NewParser()
	token, err := parser.Parse(tokenString, func(t *golangjwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*golangjwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		// Extract service_id from claims to look up the right public key
		claims, ok := t.Claims.(golangjwt.MapClaims)
		if !ok {
			return nil, fmt.Errorf("invalid claims")
		}
		serviceID, _ := claims["service_id"].(string)
		if serviceID == "" {
			return nil, fmt.Errorf("missing service_id in claims")
		}
		svc, err := cfg.GetService(serviceID)
		if err != nil {
			return nil, err
		}
		return svc.PublicKey(), nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	claims, ok := token.Claims.(golangjwt.MapClaims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// ExtractBearer extracts a Bearer token from the Authorization header.
func ExtractBearer(header string) string {
	const prefix = "Bearer "
	if len(header) > len(prefix) && header[:len(prefix)] == prefix {
		return header[len(prefix):]
	}
	return ""
}
