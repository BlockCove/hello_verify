# Hello Verify — 链下签名，链上验证

一个最小可用的「链下签名、链上验证」示例项目：

- **backend/**：Go (Gin) 后端，用 `.env` 里的私钥对数据签名，提供 `POST /sign` 接口
- **contracts/**：Solidity 验证合约，部署后校验签名是否来自 admin

```
┌─────────────┐  POST /sign        ┌──────────────────┐
│  调用方/DApp │ ─────────────────▶ │  Go 后端 (Gin)    │  私钥在 .env
└─────────────┘                    └────────┬─────────┘
       │ 拿到 data + signature              │ keccak256 + ECDSA 签名
       ▼                                    ▼
┌──────────────────────────────────────────────────────┐
│             SignatureVerifier 合约（链上）            │
│      verify(data, signature) → 签名者 == admin ?      │
└──────────────────────────────────────────────────────┘
```

**签名哈希（前后端约定一致，非常重要）**

```
messageHash = keccak256(abi.encodePacked(data, CHAIN_ID, address(this)))
```

- `data`：32 字节（bytes32）
- `CHAIN_ID`：uint256，合约部署时记录 `block.chainid`
- `address(this)`：合约地址

哈希中带上 chainId 和合约地址，防止把 A 链 / A 合约的签名拿到 B 链 / B 合约重放。

## 目录结构

```
.
├── backend/
│   ├── main.go          # Gin 签名服务
│   ├── main_test.go     # 单元测试（哈希布局 + 签名/恢复回环）
│   ├── go.mod / go.sum
│   ├── .env             # 你的私钥等配置（不提交 git）
│   └── .env.example     # 配置模板
├── contracts/
│   └── SignatureVerifier.sol   # 验证合约（使用 OpenZeppelin ECDSA 库），可直接粘贴进 Remix
├── test/
│   └── SignatureVerifier.test.js  # Hardhat 3 合约测试（node:test + viem）
├── hardhat.config.js    # Hardhat 3 配置
├── package.json         # Hardhat 3 / viem 依赖
├── .gitignore
└── README.md
```

## 快速开始

### 1. 启动后端

```bash
cd backend
cp .env.example .env
# 编辑 .env，填上 PRIVATE_KEY（这就是链上合约的 admin 私钥）
go run .
```

接口：

- `GET /signer`：查看签名者地址（部署合约时把它设为 admin）

```bash
curl http://localhost:8080/signer
# {"signer":"0xf39F...","defaultChainId":"31337","defaultContract":"0x..."}
```

- `POST /sign`：签名

```bash
curl -X POST http://localhost:8080/sign \
  -H 'Content-Type: application/json' \
  -d '{
    "data": "0x000000000000000000000000000000000000000000000000000000000000002a",
    "chainId": "31337",
    "contractAddress": "0x5FbDB2315678afecb367f032d93F642f64180aa3"
  }'
```

响应：

```json
{
  "signer": "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266",
  "data": "0x0000...2a",
  "chainId": "31337",
  "contractAddress": "0x5FbD...aa3",
  "messageHash": "0x...",
  "signature": "0x...(65字节)"
}
```

> `chainId` / `contractAddress` 也可以不传，会使用 `.env` 里的默认值。

### 2. 部署合约（Remix）

1. 打开 [remix.ethereum.org](https://remix.ethereum.org)
2. 左侧新建文件 `SignatureVerifier.sol`，粘贴 `contracts/SignatureVerifier.sol` 的内容
3. 切到 Solidity Compiler 编译（0.8.20 及以上）。合约 import 了 `@openzeppelin/contracts` 的 ECDSA 库，Remix 编译时会自动联网解析，无需手动下载
4. 切到 Deploy & Run Transactions：
   - Environment 选 `Injected Provider - MetaMask`（测试网）或 `Remix VM`（本地模拟）
   - 构造函数参数 `initialAdmin` 填后端 `GET /signer` 返回的地址
   - 点 Deploy
5. 记录部署后的**合约地址**和**chainId**（Remix VM 默认 31337；MetaMask 看当前网络）

### 3. 联调验证

1. 用真实合约地址 + chainId 调一次 `POST /sign`，拿到 `signature`
2. 回到 Remix，展开合约，调用 `verify`：
   - `data`：签名的 bytes32（如 `0x0000...2a`）
   - `signature`：后端返回的 65 字节签名
3. 返回 `(true, 签名者地址)` 即验证通过；换一个 data 再验会返回 false

辅助调试函数：

- `recoverSigner(data, signature)`：单独看恢复出的签名者地址
- `messageHash(data)`：和后端返回的 messageHash 对账
- `setAdmin(newAdmin)`：更换管理员（仅当前 admin 可调）

## 本地编译与测试（Hardhat 3）

除了 Remix，也可以在本地用 Hardhat 3 编译合约、跑测试：

```bash
npm install        # 安装 hardhat / viem / openzeppelin 依赖
npx hardhat test   # 编译合约并跑测试
```

测试内容包括：

- messageHash 与 Go 后端 API 实际输出的交叉比对（保证两端哈希布局一致）
- admin 签名通过 verify / 错误 data 拒绝 / 非 admin 签名拒绝
- setAdmin 权限校验

> 只想检查编译的话，跑 `npx hardhat compile` 即可。

## 玩法示例

**场景：后端发放积分 / 白名单资格**

1. 用户在前端完成某个任务（答题、游戏得分等）
2. 后端校验业务逻辑后，把结果打包成 bytes32：
   `data = keccak256(abi.encode(user, score, nonce))`，用 admin 私钥签名
3. 后端把 `data + signature` 返回给用户
4. 用户在链上调用业务合约，合约 `verify` 通过后发放积分/资格

**业务合约配合领取的示例**（加上防重放）：

```solidity
SignatureVerifier public verifier;          // 部署好的验证合约
mapping(bytes32 => bool) public usedSignatures;

function claim(bytes32 data, bytes calldata signature) external {
    (bool valid, ) = verifier.verify(data, signature);
    require(valid, "invalid signature");
    require(!usedSignatures[data], "already used"); // 防同一签名重复领取
    usedSignatures[data] = true;
    // 发放奖励...
}
```

## EIP-712 的玩法与区别

本项目用的是**普通 keccak 哈希签名**（与参考代码一致）。生产项目更推荐 **EIP-712 结构化签名**。

**EIP-712 是什么**：把签名内容定义为带类型的结构化数据（domain + struct），签名前先计算
`keccak256("\x19\x01" ‖ domainSeparator ‖ structHash)`。钱包（如 MetaMask）可以解析并**明文展示**你要签的内容。

**区别对比**：

| | 本项目（普通 keccak） | EIP-712 |
|---|---|---|
| 消息哈希 | `keccak256(abi.encodePacked(data, chainId, address))` | `keccak256("\x19\x01" ‖ domainSeparator ‖ structHash)` |
| 钱包可读性 | 一串 hex，用户看不懂 | 显示结构化字段（name/version/chainId/数据） |
| 防钓鱼 | 靠自定义规则做域分离 | 钱包统一展示 domain，用户能识别伪造站点 |
| 实现复杂度 | 低 | 中 |
| 适用场景 | demo、内部系统 | 面向用户签名、跨 dApp 的标准场景 |

**升级成 EIP-712 的关键改动**（示意代码，非本项目实现）：

Go 端：

```go
import (
    "github.com/ethereum/go-ethereum/common/math"
    "github.com/ethereum/go-ethereum/signer/core/apitypes"
)

typedData := apitypes.TypedData{
    Types: apitypes.Types{
        "EIP712Domain": {
            {Name: "name", Type: "string"},
            {Name: "version", Type: "string"},
            {Name: "chainId", Type: "uint256"},
            {Name: "verifyingContract", Type: "address"},
        },
        "SignData": {{Name: "data", Type: "bytes32"}},
    },
    PrimaryType: "SignData",
    Domain: apitypes.TypedDataDomain{
        Name:              "SignatureVerifier",
        Version:           "1",
        ChainId:           math.NewHexOrDecimal256(31337),
        VerifyingContract: contractAddr.Hex(),
    },
    Message: apitypes.TypedDataMessage{"data": dataHex},
}
domainSeparator, _ := typedData.HashStruct("EIP712Domain", typedData.Domain.Map())
structHash, _ := typedData.HashStruct("SignData", typedData.Message)
digest := crypto.Keccak256([]byte{0x19, 0x01}, domainSeparator, structHash)
sig, _ := crypto.Sign(digest, privKey)
```

合约端：

```solidity
import {ECDSA} from "@openzeppelin/contracts/utils/cryptography/ECDSA.sol";

using ECDSA for bytes32;

bytes32 private constant DOMAIN_TYPEHASH =
    keccak256("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)");
bytes32 private constant SIGN_DATA_TYPEHASH = keccak256("SignData(bytes32 data)");

function verifyEIP712(bytes32 data, bytes calldata signature) external view returns (bool, address) {
    bytes32 separator = keccak256(abi.encode(
        DOMAIN_TYPEHASH, keccak256("SignatureVerifier"), keccak256("1"), CHAIN_ID, address(this)));
    bytes32 digest = keccak256(abi.encodePacked(
        "\x19\x01", separator, keccak256(abi.encode(SIGN_DATA_TYPEHASH, data))));
    address signer = digest.recover(signature);
    return (signer == admin, signer);
}
```

## 安全注意事项

1. **私钥安全**：`PRIVATE_KEY` 只放后端 `.env`（`.gitignore` 已排除）；绝不放前端、绝不提交仓库
2. **签名可塑性**：OpenZeppelin ECDSA 库已校验 `v ∈ {27, 28}` 且 `s` 在下半区，避免同一哈希被改造成另一条合法签名
3. **重放攻击**：
   - 跨链 / 跨合约重放：哈希里已含 `chainId + 合约地址` ✅
   - 同合约重复提交：需要业务合约加 `usedSignatures` mapping + nonce（见玩法示例）⚠️
4. 生产环境建议升级 EIP-712，并给签名加过期时间（如把 deadline 打包进 data）

## 常见问题

- **verify 返回 false？** 逐项检查：chainId 是否一致（合约记录的是部署时的 `block.chainid`）、合约地址是否一致、data 是否为同一个 bytes32
- **Remix 里 data 怎么填？** bytes32 是 32 字节 hex：`0x` + 64 个字符，不足前面补 0
- **签名者地址怎么算？** 后端 `GET /signer` 返回的就是，部署时把它传给构造函数作为 admin
- **换了一条链要重新部署吗？** 要。`CHAIN_ID` 是合约部署时快照的，换链必须重新部署
