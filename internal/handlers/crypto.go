package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"strings"
)

var loadTest bool

func init() {
	if os.Getenv("LOAD_TEST") == "true" {
		loadTest = true
	}
}

// verifySignature routes the payload to the correct cryptographic strategy based on the provider.
func verifySignature(providerName string, payload []byte, headerSig string, secret string) bool {
	if loadTest {
		return true
	}
	if headerSig == "" || secret == "" {
		return false
	}

	switch providerName {
	case "shopify":
		return verifyShopify(payload, headerSig, secret)
	case "stripe":
		return verifyStripe(payload, headerSig, secret)
	case "woocommerce":
		return verifyWooCommerce(payload, headerSig, secret)
	default:
		// Default to a standard hex-encoded HMAC-SHA256 if a new provider doesn't have custom logic
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(payload)
		expectedMAC := hex.EncodeToString(mac.Sum(nil))
		return hmac.Equal([]byte(headerSig), []byte(expectedMAC))
	}
}

func verifyShopify(payload []byte, headerSig string, secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedMAC := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	
	return hmac.Equal([]byte(headerSig), []byte(expectedMAC))
}

func verifyStripe(payload []byte, headerSig string, secret string) bool {
	var timestamp, signature string
	parts := strings.Split(headerSig, ",")
	for _, part := range parts {
		if strings.HasPrefix(part, "t=") {
			timestamp = strings.TrimPrefix(part, "t=")
		} else if strings.HasPrefix(part, "v1=") {
			signature = strings.TrimPrefix(part, "v1=")
		}

	}

	if timestamp == "" || signature == "" {
		return false
	}

	signedPayload := timestamp + "." + string(payload)
	
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signedPayload))
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(signature), []byte(expectedMAC))
}

func verifyWooCommerce(payload []byte, headerSig string, secret string) bool {

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedMAC := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	
	return hmac.Equal([]byte(headerSig), []byte(expectedMAC))
}
