package main

import (
	"bytes"
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

// SignHash 对 32 字节摘要做 ECDSA 签名，返回 65 字节 [r || s || v]，
// v 已归一化为 27/28（Solidity ecrecover 的要求，go-ethereum 默认是 0/1）
func (s *Signer) SignHash(hash []byte) ([]byte, error) {
	sig, err := crypto.Sign(hash, s.privKey)
	if err != nil {
		return nil, err
	}
	if sig[64] < 27 {
		sig[64] += 27
	}
	return sig, nil
}

// Sign 普通 keccak 方案的签名（/sign 路由使用）
func (s *Signer) Sign(data [32]byte, chainID *big.Int, contract common.Address) ([]byte, error) {
	return s.SignHash(MessageHash(data, chainID, contract))
}

// MessageHash 与 SignatureVerifier 合约端完全一致：
// keccak256(abi.encodePacked(data, chainId(uint256), contractAddress(address)))
// 哈希中包含 chainId 与合约地址，防止跨链 / 跨合约重放。
func MessageHash(data [32]byte, chainID *big.Int, contract common.Address) []byte {
	packed := make([]byte, 0, 32+32+20)
	packed = append(packed, data[:]...)
	packed = append(packed, common.LeftPadBytes(chainID.Bytes(), 32)...)
	packed = append(packed, contract.Bytes()...)
	return crypto.Keccak256(packed)
}

// ---------- EIP-712（与 SignatureVerifier712 合约端一致） ----------

var (
	eip712DomainTypehash    = crypto.Keccak256Hash([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"))
	eip712SignDataTypehash  = crypto.Keccak256Hash([]byte("SignData(bytes32 data)"))
	eip712NameHash          = crypto.Keccak256Hash([]byte("SignatureVerifier"))
	eip712VersionHash       = crypto.Keccak256Hash([]byte("1"))
)

// EIP712Digest 计算 EIP-712 摘要（/sign712 路由使用）：
// keccak256("\x19\x01" ‖ domainSeparator ‖ structHash)
// domainSeparator = keccak256(abi.encode(DOMAIN_TYPEHASH, nameHash, versionHash, chainId, verifyingContract))
// structHash      = keccak256(abi.encode(SIGN_DATA_TYPEHASH, data))
func EIP712Digest(data [32]byte, chainID *big.Int, contract common.Address) []byte {
	domainSeparator := crypto.Keccak256Hash(bytes.Join([][]byte{
		eip712DomainTypehash[:],
		eip712NameHash[:],
		eip712VersionHash[:],
		common.LeftPadBytes(chainID.Bytes(), 32),
		common.LeftPadBytes(contract.Bytes(), 32), // abi.encode 中 address 左填充为 32 字节
	}, nil))
	structHash := crypto.Keccak256Hash(bytes.Join([][]byte{
		eip712SignDataTypehash[:],
		data[:],
	}, nil))

	packed := make([]byte, 0, 2+32+32)
	packed = append(packed, 0x19, 0x01)
	packed = append(packed, domainSeparator[:]...)
	packed = append(packed, structHash[:]...)
	return crypto.Keccak256(packed)
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

// signParams 解析校验后的签名参数，/sign 与 /sign712 共用
type signParams struct {
	data     [32]byte
	chainID  *big.Int
	contract common.Address
}

// parseSignParams 解析并校验请求，失败时已写入错误响应并返回 ok=false
func parseSignParams(c *gin.Context, defaultChainID *big.Int, defaultContract common.Address) (*signParams, bool) {
	var req signRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体非法: " + err.Error()})
		return nil, false
	}

	params := &signParams{}

	var err error
	if params.data, err = parseBytes32(req.Data); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, false
	}

	params.chainID = defaultChainID
	if req.ChainID != "" {
		if params.chainID, err = parseChainID(req.ChainID); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return nil, false
		}
	}
	if params.chainID == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "未提供 chainId（请求里传，或配置 .env 的 CHAIN_ID）"})
		return nil, false
	}

	params.contract = defaultContract
	if req.ContractAddress != "" {
		if params.contract, err = parseAddress(req.ContractAddress); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return nil, false
		}
	}
	if params.contract == (common.Address{}) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "未提供 contractAddress（请求里传，或配置 .env 的 CONTRACT_ADDRESS）"})
		return nil, false
	}

	return params, true
}

// hashFunc 计算待签名的摘要
type hashFunc func(data [32]byte, chainID *big.Int, contract common.Address) []byte

// newSignHandler 返回签名处理器，hashFn 决定使用普通 keccak 还是 EIP-712 方案
func newSignHandler(signer *Signer, defaultChainID *big.Int, defaultContract common.Address, hashFn hashFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		params, ok := parseSignParams(c, defaultChainID, defaultContract)
		if !ok {
			return
		}

		hash := hashFn(params.data, params.chainID, params.contract)
		sig, err := signer.SignHash(hash)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "签名失败: " + err.Error()})
			return
		}

		c.JSON(http.StatusOK, signResponse{
			Signer:          signer.Address(),
			Data:            "0x" + hex.EncodeToString(params.data[:]),
			ChainID:         params.chainID.String(),
			ContractAddress: params.contract,
			MessageHash:     "0x" + hex.EncodeToString(hash),
			Signature:       "0x" + hex.EncodeToString(sig),
		})
	}
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
		defaultChainID     *big.Int
		defaultContract    common.Address
		defaultContract712 common.Address
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
	if v := os.Getenv("CONTRACT_ADDRESS_712"); v != "" {
		if defaultContract712, err = parseAddress(v); err != nil {
			log.Fatal(err)
		}
	}
	if defaultContract712 == (common.Address{}) {
		defaultContract712 = defaultContract // 未配置 712 合约地址时回退到普通版
	}

	// 712 签名私钥（未配置时回退到普通版 PRIVATE_KEY）
	signer712 := signer
	if v := os.Getenv("PRIVATE_KEY_712"); v != "" {
		if signer712, err = NewSigner(v); err != nil {
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
			"signer":             signer.Address(),
			"signer712":          signer712.Address(),
			"defaultChainId":     chainIDStr,
			"defaultContract":    defaultContract,
			"defaultContract712": defaultContract712,
		})
	})

	// 普通 keccak 方案，配合 contracts/SignatureVerifier.sol
	r.POST("/sign", newSignHandler(signer, defaultChainID, defaultContract, MessageHash))
	// EIP-712 方案，配合 contracts/SignatureVerifier712.sol（可用独立的私钥/合约地址）
	r.POST("/sign712", newSignHandler(signer712, defaultChainID, defaultContract712, EIP712Digest))

	log.Printf("签名服务已启动: http://localhost:%s  signer=%s", port, signer.Address())
	if err := r.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}
