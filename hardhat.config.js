import { defineConfig } from "hardhat/config";
import hardhatToolboxViem from "@nomicfoundation/hardhat-toolbox-viem";

export default defineConfig({
  solidity: "0.8.35",
  plugins: [hardhatToolboxViem],
});
