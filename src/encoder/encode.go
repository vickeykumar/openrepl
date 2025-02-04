package encoder

import (
	"encoding/base64"
	"log"
	"crypto/aes"
    "crypto/cipher"
    "crypto/rand"
    "math/big"
    "io"
    "fmt"
)

// use 16 byte, default 128 bit for AES
const SECRET_BITSIZE = 16*8

func GenerateLargePrime(bits ...int) *big.Int {
     var bitsize int = SECRET_BITSIZE
     if len(bits) > 0 {
        bitsize = bits[0]
     }

    // Generate a random prime number
    prime, err := rand.Prime(rand.Reader, bitsize)
    if err != nil {
        log.Println("Error:", err)
        // any prime number, probably we won't reach here
        return  big.NewInt(65537)
    }
    
    return prime
}

var RawURLEncoding = base64.URLEncoding.WithPadding(base64.NoPadding)
var smallsecret *big.Int = GenerateLargePrime(16)


func EncodePID(pid interface{}) string {
        ppid, _ := pid.(int)
        message := big.NewInt(int64(ppid))
        message = message.Mul(message, smallsecret)
        // send the encoded Bytes
        return RawURLEncoding.EncodeToString(message.Bytes())
}

func DecodeToPID(jid string) int {
	if jid != "" {
        	pidbytes,err := RawURLEncoding.DecodeString(jid)
        	if err == nil {
                	decoded := new(big.Int).SetBytes(pidbytes)
                	decoded.Div(decoded, smallsecret)
                	return int(decoded.Int64())
        	}
        	log.Println("invalid pid recieved :"+jid, string(pidbytes), err.Error())
	}
        return -1
}

// Encrypt encrypts the plaintext using AES-GCM with the given key and returns a URL-safe base64-encoded ciphertext.
func Encrypt(plainTextBytes, keyBytes []byte) ([]byte, error) {

    block, err := aes.NewCipher(keyBytes)
    if err != nil {
        return []byte(""), err
    }

    aesGCM, err := cipher.NewGCM(block)
    if err != nil {
        return []byte(""), err
    }

    nonce := make([]byte, aesGCM.NonceSize())
    if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
        return []byte(""), err
    }

    cipherText := aesGCM.Seal(nonce, nonce, plainTextBytes, nil)
    urlSafeCipherText := RawURLEncoding.EncodeToString(cipherText)
    return []byte(urlSafeCipherText), nil
}

// Decrypt decrypts the URL-safe base64-encoded ciphertext using AES-GCM with the given key.
func Decrypt(cipherText, keyBytes []byte) ([]byte, error) {
    cipherTextBytes, err := RawURLEncoding.DecodeString(string(cipherText))
    if err != nil {
        return []byte(""), err
    }

    block, err := aes.NewCipher(keyBytes)
    if err != nil {
        return []byte(""), err
    }

    aesGCM, err := cipher.NewGCM(block)
    if err != nil {
        return []byte(""), err
    }

    nonceSize := aesGCM.NonceSize()
    if len(cipherTextBytes) < nonceSize {
        return []byte(""), fmt.Errorf("ciphertext too short")
    }

    nonce, cipherTextBytes := cipherTextBytes[:nonceSize], cipherTextBytes[nonceSize:]
    plainTextBytes, err := aesGCM.Open(nil, nonce, cipherTextBytes, nil)
    if err != nil {
        return []byte(""), err
    }

    return plainTextBytes, nil
}
