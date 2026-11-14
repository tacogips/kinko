package kinko

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestDecryptBlobRejectsInvalidNonceLengths(t *testing.T) {
	key := make([]byte, dekLength)
	for _, aad := range []string{"", aeadContextVaultData} {
		blobJSON, err := encryptBlobWithAAD(key, []byte("synthetic vault data"), []byte(aad))
		if err != nil {
			t.Fatal(err)
		}
		for _, size := range []int{0, 1, 11, 13, 24} {
			t.Run(fmt.Sprintf("aad=%q/nonce=%d", aad, size), func(t *testing.T) {
				var blob encryptedBlob
				if err := json.Unmarshal([]byte(blobJSON), &blob); err != nil {
					t.Fatal(err)
				}
				blob.NonceB64 = base64.StdEncoding.EncodeToString(make([]byte, size))
				malformed, err := json.Marshal(blob)
				if err != nil {
					t.Fatal(err)
				}
				plain, err := decryptBlobWithAAD(key, string(malformed), []byte(aad))
				if !errors.Is(err, errDecryptFailed) || plain != nil {
					t.Fatalf("malformed nonce: plain=%q err=%v, want decryption failure", plain, err)
				}
			})
		}
	}
}
