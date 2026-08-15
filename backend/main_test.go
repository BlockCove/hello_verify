package main

import (
	"bytes"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// 固定测试私钥（Hardhat 第一个账户），保证结果可复现
const testPrivKeyHex = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"

func TestMessageHashLayout(t *testing.T) {
	// 手动按 abi.encodePacked(data(32) || chainId(32) || address(20)) 拼装，
	// 锁定与合约端一致的字节布局
	data := [32]byte{0: 0x11, 31: 0x2a}
	chainID := big.NewInt(31337)
	contract := common.HexToAddress("0x5FbDB2315678afecb367f032d93F642f64180aa3")

	manual := append(append(data[:], common.LeftPadBytes(chainID.Bytes(), 32)...), contract.Bytes()...)
	if len(manual) != 84 {
		t.Fatalf("encodePacked 长度应为 84，实际 %d", len(manual))
	}
	want := crypto.Keccak256(manual)
	if got := MessageHash(data, chainID, contract); string(got) != string(want) {
		t.Fatalf("哈希不一致:\nwant %x\ngot  %x", want, got)
	}
}

func TestSignAndRecover(t *testing.T) {
	signer, err := NewSigner(testPrivKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	wantAddr := common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")
	if signer.Address() != wantAddr {
		t.Fatalf("测试私钥对应地址应为 %s，得到 %s", wantAddr, signer.Address())
	}

	data := [32]byte{31: 0x2a}
	chainID := big.NewInt(31337)
	contract := common.HexToAddress("0x5FbDB2315678afecb367f032d93F642f64180aa3")

	sig, err := signer.Sign(data, chainID, contract)
	if err != nil {
		t.Fatal(err)
	}
	if len(sig) != 65 {
		t.Fatalf("签名长度 %d，应为 65", len(sig))
	}
	if v := sig[64]; v != 27 && v != 28 {
		t.Fatalf("v = %d，应为 27 或 28", v)
	}

	// 模拟合约端 ecrecover：go-ethereum 的 SigToPub 要求 v 为 0/1，先转回去
	raw := make([]byte, 65)
	copy(raw, sig)
	raw[64] -= 27

	pub, err := crypto.SigToPub(MessageHash(data, chainID, contract), raw)
	if err != nil {
		t.Fatalf("恢复公钥失败: %v", err)
	}
	if got := crypto.PubkeyToAddress(*pub); got != signer.Address() {
		t.Fatalf("恢复地址 %s 与签名者 %s 不一致", got.Hex(), signer.Address().Hex())
	}
}

func TestEIP712DigestKnownVector(t *testing.T) {
	// 该摘要由后端 /sign712 冒烟测试实际产出；
	// Hardhat 测试中另用 viem 的 hashTypedData 独立复算同一常量，
	// 两套独立实现一致即证明摘要符合 EIP-712 规范。
	data := [32]byte{31: 0x2a}
	chainID := big.NewInt(31337)
	contract := common.HexToAddress("0x5FbDB2315678afecb367f032d93F642f64180aa3")
	want := common.HexToHash("0x33fbb21e1ab576e8d8967b86bac2fe53a58d88251f2c6f0aeb3baf78b3a2d931")

	got := EIP712Digest(data, chainID, contract)
	if !bytes.Equal(got, want[:]) {
		t.Fatalf("EIP-712 摘要不一致:\nwant %s\ngot  %x", want.Hex(), got)
	}
}

func TestParseBytes32(t *testing.T) {
	ok := "0x" + "11" + strings.Repeat("00", 31)
	got, err := parseBytes32(ok)
	if err != nil {
		t.Fatalf("合法输入解析失败: %v", err)
	}
	if got[0] != 0x11 || got[31] != 0x00 {
		t.Fatalf("解析结果错误: %x", got)
	}

	bad := []string{"", "0x1234", "0xzz", "0x" + strings.Repeat("00", 33), "1234"}
	for _, in := range bad {
		if _, err := parseBytes32(in); err == nil {
			t.Errorf("parseBytes32(%q) 应返回错误", in)
		}
	}
}
