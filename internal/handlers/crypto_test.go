package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

func TestVerifySignature(t *testing.T) {

	secret := "test_secret_key"
	payload := []byte(`{"order_id": 12345, "status": "paid"}`)


	// Pre-compute valid signatures
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	validShopifySig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	stripeTimestamp := "1620000000"
	stripeSignedPayload := stripeTimestamp + "." + string(payload)
	macStripe := hmac.New(sha256.New, []byte(secret))
	macStripe.Write([]byte(stripeSignedPayload))
	validStripeSig := "t=" + stripeTimestamp + ",v1=" + hex.EncodeToString(macStripe.Sum(nil))

	tests := []struct {
		name         string
		provider     string
		payload      []byte
		headerSig    string
		secret       string
		expectedPass bool
	}{
		{
			name:         "Valid Shopify Signature",
			provider:     "shopify",
			payload:      payload,
			headerSig:    validShopifySig,
			secret:       secret,
			expectedPass: true,
		},
		{
			name:         "Invalid Shopify Signature (Tampered Payload)",
			provider:     "shopify",
			payload:      []byte(`{"order_id": 12345, "status": "refunded"}`),
			headerSig:    validShopifySig,
			secret:       secret,
			expectedPass: false,
		},
		{
			name:         "Valid Stripe Signature",
			provider:     "stripe",
			payload:      payload,
			headerSig:    validStripeSig,
			secret:       secret,
			expectedPass: true,
		},
		{
			name:         "Invalid Stripe Signature (Bad Secret)",
			provider:     "stripe",
			payload:      payload,
			headerSig:    validStripeSig,
			secret:       "wrong_secret",
			expectedPass: false,
		},
		{
			name:         "Missing Header Signature",
			provider:     "shopify",
			payload:      payload,

			headerSig:    "",
			secret:       secret,
			expectedPass: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := verifySignature(tt.provider, tt.payload, tt.headerSig, tt.secret)
			if result != tt.expectedPass {
				t.Errorf("verifySignature() = %v, want %v", result, tt.expectedPass)
			}
		})

	}
}
