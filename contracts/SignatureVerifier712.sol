// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import {ECDSA} from "@openzeppelin/contracts/utils/cryptography/ECDSA.sol";

/// @title SignatureVerifier712
/// @notice EIP-712 版本的链下签名、链上验证合约：只有 admin 的 EIP-712 签名能通过 verify。
/// @dev domain: name=SignatureVerifier, version=1, chainId, verifyingContract
///      签名类型: SignData(bytes32 data)
///      签名摘要 = keccak256("\x19\x01" ‖ domainSeparator ‖ structHash)，
///      与后端 POST /sign712 的 messageHash 完全一致。
///      钱包（如 MetaMask）可以解析并明文展示签名内容，防钓鱼。
contract SignatureVerifier712 {
    using ECDSA for bytes32;

    bytes32 private constant DOMAIN_TYPEHASH =
        keccak256("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)");
    bytes32 private constant SIGN_DATA_TYPEHASH = keccak256("SignData(bytes32 data)");
    bytes32 private constant NAME_HASH = keccak256("SignatureVerifier");
    bytes32 private constant VERSION_HASH = keccak256("1");

    /// @notice 部署时记录的 chainId，参与 domain separator
    uint256 public immutable CHAIN_ID;

    /// @notice 管理员地址，签名必须是该地址签出的
    address public admin;

    event AdminChanged(address indexed oldAdmin, address indexed newAdmin);

    constructor(address initialAdmin) {
        require(initialAdmin != address(0), "SignatureVerifier712: zero admin");
        admin = initialAdmin;
        CHAIN_ID = block.chainid;
    }

    modifier onlyAdmin() {
        require(msg.sender == admin, "SignatureVerifier712: not admin");
        _;
    }

    /// @notice 更换管理员，仅当前 admin 可调用
    function setAdmin(address newAdmin) external onlyAdmin {
        require(newAdmin != address(0), "SignatureVerifier712: zero admin");
        emit AdminChanged(admin, newAdmin);
        admin = newAdmin;
    }

    /// @notice EIP-712 domain separator
    function domainSeparator() public view returns (bytes32) {
        return keccak256(abi.encode(DOMAIN_TYPEHASH, NAME_HASH, VERSION_HASH, CHAIN_ID, address(this)));
    }

    /// @notice 计算 EIP-712 签名摘要（与后端一致，可用来对账）
    function digest(bytes32 data) public view returns (bytes32) {
        return keccak256(abi.encodePacked("\x19\x01", domainSeparator(), keccak256(abi.encode(SIGN_DATA_TYPEHASH, data))));
    }

    /// @notice 从 EIP-712 签名中恢复出签名者地址
    function recoverSigner(bytes32 data, bytes calldata signature) public view returns (address) {
        return digest(data).recover(signature);
    }

    /// @notice 验证 EIP-712 签名是否来自 admin，返回 (是否通过, 实际签名者)
    function verify(bytes32 data, bytes calldata signature) external view returns (bool valid, address signer) {
        signer = recoverSigner(data, signature);
        valid = signer == admin;
    }
}
