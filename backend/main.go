package main

import (
	"crypto/ecdsa"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

// Signer 持有 admin 私钥，生成与链上一致的签名
type Signer struct {
	privKey *ecdsa.PrivateKey
	address common.Address
}

// NewSigner 从十六进制私钥创建签名器
func NewSigner(privKeyHex string) (*Signer, error) {
	privKey, err := crypto.HexToECDSA(strings.TrimPrefix(privKeyHex, "0x"))
	if err != nil {
		return nil, fmt.Errorf("私钥格式错误: %w", err)
	}
	return &Signer{
		privKey: privKey,
		address: crypto.PubkeyToAddress(privKey.PublicKey),
	}, nil
}

// Address 返回签名者地址，即链上合约的 admin
func (s *Signer) Address() common.Address { return s.address }

// MessageHash 与合约端完全一致：
// keccak256(abi.encodePacked(data, chainId(uint256), contractAddress(address)))
// 哈希中包含 chainId 与合约地址，防止跨链 / 跨合约重放。
func MessageHash(data [32]byte, chainID *big.Int, contract common.Address) []byte {
	packed := make([]byte, 0, 32+32+20)
	packed = append(packed, data[:]...)
	packed = append(packed, common.LeftPadBytes(chainID.Bytes(), 32)...)
	packed = append(packed, contract.Bytes()...)
	return crypto.Keccak256(packed)
}

// Sign 对 data 签名，返回 65 字节 [r || s || v]，
// v 已归一化为 27/28（Solidity ecrecover 的要求，go-ethereum 默认是 0/1）
func (s *Signer) Sign(data [32]byte, chainID *big.Int, contract common.Address) ([]byte, error) {
	sig, err := crypto.Sign(MessageHash(data, chainID, contract), s.privKey)
	if err != nil {
		return nil, err
	}
	if sig[64] < 27 {
		sig[64] += 27
	}
	return sig, nil
}

// ---------- 参数解析 ----------

func parseBytes32(hexStr string) ([32]byte, error) {
	var out [32]byte
	b, err := hex.DecodeString(strings.TrimPrefix(hexStr, "0x"))
	if err != nil {
		return out, errors.New("data 必须是十六进制字符串")
	}
	if len(b) != 32 {
		return out, fmt.Errorf("data 必须是 32 字节（64 位 hex），当前 %d 字节", len(b))
	}
	copy(out[:], b)
	return out, nil
}

func parseChainID(s string) (*big.Int, error) {
	chainID, ok := new(big.Int).SetString(s, 10)
	if !ok || chainID.Sign() < 0 {
		return nil, errors.New("chainId 必须是十进制非负整数")
	}
	return chainID, nil
}

func parseAddress(s string) (common.Address, error) {
	if !common.IsHexAddress(s) {
		return common.Address{}, errors.New("地址格式非法，应为 0x + 40 位 hex")
	}
	return common.HexToAddress(s), nil
}

// ---------- HTTP 接口 ----------

type signRequest struct {
	Data            string `json:"data" binding:"required"` // 0x + 64 位 hex（32 字节）
	ChainID         string `json:"chainId"`                 // 十进制字符串，缺省用 .env 的 CHAIN_ID
	ContractAddress string `json:"contractAddress"`         // 缺省用 .env 的 CONTRACT_ADDRESS
}

type signResponse struct {
	Signer          common.Address `json:"signer"`
	Data            string         `json:"data"`
	ChainID         string         `json:"chainId"`
	ContractAddress common.Address `json:"contractAddress"`
	MessageHash     string         `json:"messageHash"`
	Signature       string         `json:"signature"`
}

func main() {
	_ = godotenv.Load() // .env 不存在时不报错（也支持直接 export 环境变量）

	privKeyHex := os.Getenv("PRIVATE_KEY")
	if privKeyHex == "" {
		log.Fatal("缺少 PRIVATE_KEY，请参考 .env.example 配置 .env")
	}
	signer, err := NewSigner(privKeyHex)
	if err != nil {
		log.Fatal(err)
	}

	// 可选配置：chainId / 合约地址缺省值（请求里可覆盖）
	var (
		defaultChainID  *big.Int
		defaultContract common.Address
	)
	if v := os.Getenv("CHAIN_ID"); v != "" {
		if defaultChainID, err = parseChainID(v); err != nil {
			log.Fatal(err)
		}
	}
	if v := os.Getenv("CONTRACT_ADDRESS"); v != "" {
		if defaultContract, err = parseAddress(v); err != nil {
			log.Fatal(err)
		}
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	r := gin.Default()

	// 查看当前签名者地址（部署合约时把它设为 admin）
	r.GET("/signer", func(c *gin.Context) {
		chainIDStr := ""
		if defaultChainID != nil {
			chainIDStr = defaultChainID.String()
		}
		c.JSON(http.StatusOK, gin.H{
			"signer":          signer.Address(),
			"defaultChainId":  chainIDStr,
			"defaultContract": defaultContract,
		})
	})

	r.POST("/sign", func(c *gin.Context) {
		var req signRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
			return
		}

		data, err := parseBytes32(req.Data)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		chainID := defaultChainID
		if req.ChainID != "" {
			if chainID, err = parseChainID(req.ChainID); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}
		if chainID == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "未提供 chainId（请求里传，或配置 .env 的 CHAIN_ID）"})
			return
		}

		contract := defaultContract
		if req.ContractAddress != "" {
			if contract, err = parseAddress(req.ContractAddress); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}
		if contract == (common.Address{}) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "未提供 contractAddress（请求里传，或配置 .env 的 CONTRACT_ADDRESS）"})
			return
		}

		sig, err := signer.Sign(data, chainID, contract)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "签名失败: " + err.Error()})
			return
		}

		c.JSON(http.StatusOK, signResponse{
			Signer:          signer.Address(),
			Data:            "0x" + hex.EncodeToString(data[:]),
			ChainID:         chainID.String(),
			ContractAddress: contract,
			MessageHash:     "0x" + hex.EncodeToString(MessageHash(data, chainID, contract)),
			Signature:       "0x" + hex.EncodeToString(sig),
		})
	})

	log.Printf("签名服务已启动: http://localhost:%s  signer=%s", port, signer.Address())
	if err := r.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}
