// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import {ECDSA} from "@openzeppelin/contracts/utils/cryptography/ECDSA.sol";

/// @title SignatureVerifier
/// @notice 链下签名、链上验证的最小示例：只有 admin 的签名能通过 verify。
/// @dev 签名哈希与后端完全一致：
///      keccak256(abi.encodePacked(data, CHAIN_ID, address(this)))
///      CHAIN_ID 和合约地址参与哈希，防止跨链 / 跨合约重放。
///      签名恢复使用 OpenZeppelin 的 ECDSA 库（Remix 编译时自动解析 import）。
contract SignatureVerifier {
    using ECDSA for bytes32;

    /// @notice 部署时记录的 chainId，参与签名哈希
    uint256 public immutable CHAIN_ID;

    /// @notice 管理员地址，签名必须是该地址签出的
    address public admin;

    event AdminChanged(address indexed oldAdmin, address indexed newAdmin);

    constructor(address initialAdmin) {
        require(initialAdmin != address(0), "SignatureVerifier: zero admin");
        admin = initialAdmin;
        CHAIN_ID = block.chainid;
    }

    modifier onlyAdmin() {
        require(msg.sender == admin, "SignatureVerifier: not admin");
        _;
    }

    /// @notice 更换管理员，仅当前 admin 可调用
    function setAdmin(address newAdmin) external onlyAdmin {
        require(newAdmin != address(0), "SignatureVerifier: zero admin");
        emit AdminChanged(admin, newAdmin);
        admin = newAdmin;
    }

    /// @notice 计算与后端一致的签名哈希（可用来对账）
    function messageHash(bytes32 data) public view returns (bytes32) {
        return keccak256(abi.encodePacked(data, CHAIN_ID, address(this)));
    }

    /// @notice 从签名中恢复出签名者地址
    function recoverSigner(bytes32 data, bytes calldata signature) public view returns (address) {
        return messageHash(data).recover(signature);
    }

    /// @notice 验证签名是否来自 admin，返回 (是否通过, 实际签名者)
    function verify(bytes32 data, bytes calldata signature) external view returns (bool valid, address signer) {
        signer = recoverSigner(data, signature);
        valid = signer == admin;
    }
}
