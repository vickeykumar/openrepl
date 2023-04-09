package encoder

import (
	"encoding/base64"
	"log"
	"crypto/rand"
    	"math/big"
)

// use 32 bits for type int
const SECRET_BITSIZE = 16

func GenerateLargePrime() *big.Int {
     // generate large prime

    // Generate a random prime number
    prime, err := rand.Prime(rand.Reader, SECRET_BITSIZE)
    if err != nil {
        log.Println("Error:", err)
        // any prime number, probably we won't reach here
        return  big.NewInt(65537)
    }
    
    return prime
}

var RawURLEncoding = base64.URLEncoding.WithPadding(base64.NoPadding)
var largesecret *big.Int = GenerateLargePrime()


func EncodePID(pid interface{}) string {
        ppid, _ := pid.(int)
        message := big.NewInt(int64(ppid))
        message = message.Mul(message, largesecret)
        // send the encoded Bytes
        return RawURLEncoding.EncodeToString(message.Bytes())
}

func DecodeToPID(jid string) int {
        pidbytes,err := RawURLEncoding.DecodeString(jid)
        if err == nil {
                decoded := new(big.Int).SetBytes(pidbytes)
                decoded.Div(decoded, largesecret)
                return int(decoded.Int64())
        }
        log.Println("invalid pid recieved :"+jid, string(pidbytes), err.Error())
        return -1
}


