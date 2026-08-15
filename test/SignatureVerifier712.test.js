import { describe, test, before } from "node:test";
import assert from "node:assert/strict";
import { hashTypedData, numberToHex, getAddress } from "viem";
import { privateKeyToAccount } from "viem/accounts";
import hre from "hardhat";

// Hardhat 经典测试账户 #0，与 Go 后端 .env.example 一致
const ADMIN_PK = "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80";
const ADMIN_ADDR = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266";

// EIP-712 类型定义，与合约 SignatureVerifier712 一致
const TYPES_712 = {
  SignData: [{ name: "data", type: "bytes32" }],
};

describe("SignatureVerifier712", () => {
  let contract;
  let viem;
  let adminAccount;

  before(async () => {
    viem = (await hre.network.create()).viem;
    adminAccount = privateKeyToAccount(ADMIN_PK);
    contract = await viem.deployContract("SignatureVerifier712", [ADMIN_ADDR]);
  });

  test("EIP-712 digest 与 Go 后端 /sign712 实际输出一致", () => {
    // 后端冒烟测试真实输出（data=0x...2a, chainId=31337, contract=0x5FbD...aa3）
    // viem 独立实现复算同一常量，一致即证明两端都符合 EIP-712 规范
    const got = hashTypedData({
      domain: {
        name: "SignatureVerifier",
        version: "1",
        chainId: 31337n,
        verifyingContract: "0x5FbDB2315678afecb367f032d93F642f64180aa3",
      },
      types: TYPES_712,
      primaryType: "SignData",
      message: { data: numberToHex(0x2a, { size: 32 }) },
    });
    assert.equal(
      got,
      "0x33fbb21e1ab576e8d8967b86bac2fe53a58d88251f2c6f0aeb3baf78b3a2d931",
    );
  });

  test("verify 通过 admin 的 EIP-712 签名", async () => {
    const data = numberToHex(0x2a, { size: 32 });
    const domain = {
      name: "SignatureVerifier",
      version: "1",
      chainId: await contract.read.CHAIN_ID(),
      verifyingContract: contract.address,
    };
    const signature = await adminAccount.signTypedData({
      domain,
      types: TYPES_712,
      primaryType: "SignData",
      message: { data },
    });

    const [valid, signer] = await contract.read.verify([data, signature]);
    assert.equal(valid, true);
    assert.equal(signer.toLowerCase(), ADMIN_ADDR.toLowerCase());
  });

  test("verify 拒绝错误 data", async () => {
    const data = numberToHex(0x2a, { size: 32 });
    const domain = {
      name: "SignatureVerifier",
      version: "1",
      chainId: await contract.read.CHAIN_ID(),
      verifyingContract: contract.address,
    };
    const signature = await adminAccount.signTypedData({
      domain,
      types: TYPES_712,
      primaryType: "SignData",
      message: { data },
    });

    const [valid] = await contract.read.verify([
      numberToHex(0x2b, { size: 32 }),
      signature,
    ]);
    assert.equal(valid, false);
  });

  test("verify 拒绝非 admin 的签名", async () => {
    const data = numberToHex(0x2a, { size: 32 });
    const domain = {
      name: "SignatureVerifier",
      version: "1",
      chainId: await contract.read.CHAIN_ID(),
      verifyingContract: contract.address,
    };
    // Hardhat 经典测试账户 #1
    const other = privateKeyToAccount(
      "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d",
    );
    const signature = await other.signTypedData({
      domain,
      types: TYPES_712,
      primaryType: "SignData",
      message: { data },
    });

    const [valid] = await contract.read.verify([data, signature]);
    assert.equal(valid, false);
  });

  test("setAdmin 仅 admin 可调用", async () => {
    const beef = getAddress("0x000000000000000000000000000000000000beef");
    const wallets = await viem.getWalletClients();

    // 非 admin 账户调用应 revert
    await viem.assertions.revertWith(
      contract.write.setAdmin([beef], { account: wallets[1].account }),
      "SignatureVerifier712: not admin",
    );

    // admin 调用成功（本地网络给 admin 冲点 gas，用 admin 私钥签名）
    const testClient = await viem.getTestClient();
    await testClient.setBalance({ address: ADMIN_ADDR, value: 10n ** 18n });
    await contract.write.setAdmin([beef], { account: adminAccount });

    assert.equal((await contract.read.admin()).toLowerCase(), beef.toLowerCase());
  });
});
