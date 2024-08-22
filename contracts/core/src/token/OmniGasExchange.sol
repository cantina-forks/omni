// SPDX-License-Identifier: GPL-3.0-only
pragma solidity 0.8.24;

import { OwnableUpgradeable } from "@openzeppelin/contracts-upgradeable/access/OwnableUpgradeable.sol";
import { PausableUpgradeable } from "@openzeppelin/contracts-upgradeable/utils/PausableUpgradeable.sol";
import { XAppUpgradeable } from "src/pkg/XAppUpgradeable.sol";
import { FeeOracleV1 } from "src/xchain/FeeOracleV1.sol";
import { ConfLevel } from "src/libraries/ConfLevel.sol";
import { OmniXFund } from "./OmniXFund.sol";

/**
 * @title OmniGasExchange
 * @notice A unidirectional cross-chain gas exchange, allowing users to swap ETH for OMNI on Omni.
 */
contract OmniGasExchange is XAppUpgradeable, OwnableUpgradeable, PausableUpgradeable {
    event MaxSwapSet(uint256 max);
    event FundSet(address fund);
    event PctCutSet(uint256 pct);
    event Swapped(address indexed recipient, uint256 paid, uint256 fee, uint256 cut, uint256 amtOMNI);

    /**
     * @notice Gas limit passed to OmniXFund.tryWithdrawRemaining xcall
     */
    uint64 internal constant WITHDRAW_XCALL_GAS = 100_000;

    /**
     * @notice Denominator for pct cut
     */
    uint256 internal constant PCT_CUT_DENOM = 1000;

    /**
     * @notice Address of OmniXFund on Omni
     */
    address public fund;

    /**
     * @notice Max amt (in native token) that can be swapped in a single tx
     * @dev TODO: Should we denominate this in OMNI?
     */
    uint256 public maxSwap;

    /**
     * @notice Percentage cut (over PCT_CUT_DENOM) taken by this contract for each swap
     *         Used to disencentivize spamming
     */
    uint256 public pctCut;

    /**
     * @notice Map address to total historical swap requests.
     * @dev On each swap(), the total historical amount swapped for an account is sent to OmniXFund.
     *      This allows OmniXFund to recover from failed transfers with retries. And mitigates reorg risk
     *      on ConfLevel.Latest, as accounts that receive OMNI for ETH deposits that do not remain in
     *      the canonical source will not receive OMNI for additional deposits, until total deposis
     *      are accounted for.
     */
    mapping(address => uint256) public swapped;

    constructor() {
        _disableInitializers();
    }

    struct InitParams {
        address fund;
        address portal;
        address owner;
        uint256 maxSwap;
        uint256 pctCut;
    }

    function initialize(InitParams calldata p) external initializer {
        _setFund(p.fund);
        _setMaxSwap(p.maxSwap);
        _setPctCut(p.pctCut);
        __XApp_init(p.portal, ConfLevel.Latest);
        __Ownable_init(p.owner);
    }

    /**
     * @notice Swaps `msg.value` ETH for OMNI and sends it to `recipient` on Omni.
     *
     *         Takes an xcall fee and a pct cut. Cut taken to disencentivize spamming.
     *         Returns the amount of OMNI swapped for.
     *
     *         To retry (if OmniXFund transfer fails), call swap() again with the
     *         same `recipient`, and msg.value == swapFee().
     *
     * @param recipient Address on Omni to send OMNI to
     */
    function swap(address recipient) public payable whenNotPaused returns (uint256) {
        uint256 fee = swapFee();
        require(msg.value >= fee, "OmniExchange: insufficient fee");

        uint256 amtETH = msg.value - fee;
        require(amtETH <= maxSwap, "OmniExchange: over max");

        uint256 cut = amtETH * pctCut / PCT_CUT_DENOM;
        amtETH -= cut;

        uint256 amtOMNI = ethToOmni(amtETH);
        swapped[recipient] += amtOMNI;

        xcall({
            destChainId: omniChainId(),
            to: fund,
            conf: ConfLevel.Latest,
            data: abi.encodeCall(OmniXFund.tryWithdrawRemaining, (recipient, swapped[recipient])),
            gasLimit: WITHDRAW_XCALL_GAS
        });

        emit Swapped(recipient, msg.value, fee, cut, amtOMNI);

        return amtOMNI;
    }

    /**
     * @notice xcall fee required for swap(). Does not include pct cut.
     */
    function swapFee() public view returns (uint256) {
        // Use max addrs & amount to use no zero byte calldata to ensure max fee
        // Though in Omni v1, this will not matter, as fee is not discounted for zero-byte calldata
        address recipient = address(type(uint160).max);
        uint256 amt = type(uint256).max;

        return feeFor({
            destChainId: omniChainId(),
            data: abi.encodeCall(OmniXFund.tryWithdrawRemaining, (recipient, amt)),
            gasLimit: WITHDRAW_XCALL_GAS
        });
    }

    /**
     * @notice Returns the amount of OMNI that `amtETH` msg.value would swap() to, whether or not
     *         the swap would succeed, and the revert reason, if any.
     */
    function simSwap(uint256 amtETH) public view returns (uint256, bool, string memory) {
        uint256 fee = swapFee();
        if (amtETH < fee) return (0, false, "insufficient fee");

        amtETH = amtETH - fee;
        if (amtETH > maxSwap) return (0, false, "over max");

        amtETH -= amtETH * pctCut / PCT_CUT_DENOM;

        return (ethToOmni(amtETH), true, "");
    }

    /**
     * @notice Returns true if `amtETH` can be swapped() for OMNI
     */
    function canSwap(uint256 amtETH) public view returns (bool) {
        (, bool success,) = simSwap(amtETH);
        return success;
    }

    /**
     * @notice Returns the amount of ETH needed to swap for `amtOMNI`
     */
    function ethNeededFor(uint256 amtOMNI) public view returns (uint256) {
        uint256 amtETH = omniToEth(amtOMNI);

        // undo pct cut
        amtETH = amtETH * PCT_CUT_DENOM / (PCT_CUT_DENOM - pctCut);

        // undo fee
        return amtETH + swapFee();
    }

    /**
     * @notice Converts `amtOMNI` to ETH, using the current conversion rate
     */
    function omniToEth(uint256 amtOMNI) public view returns (uint256) {
        FeeOracleV1 oracle = FeeOracleV1(omni.feeOracle());
        return amtOMNI * oracle.CONVERSION_RATE_DENOM() / oracle.toNativeRate(omniChainId());
    }

    /**
     * @notice Converts `amtETH` to OMNI, using the current conversion rate
     */
    function ethToOmni(uint256 amtETH) public view returns (uint256) {
        FeeOracleV1 oracle = FeeOracleV1(omni.feeOracle());
        return amtETH * oracle.toNativeRate(omniChainId()) / oracle.CONVERSION_RATE_DENOM();
    }

    //////////////////////////////////////////////////////////////////////////////
    //                                  Admin                                   //
    //////////////////////////////////////////////////////////////////////////////

    function pause() external onlyOwner {
        _pause();
    }

    function unpause() external onlyOwner {
        _unpause();
    }

    function withdraw(address to) external onlyOwner {
        (bool success,) = to.call{ value: address(this).balance }("");
        require(success, "OmniExchange: withdraw failed");
    }

    function setMaxSwap(uint256 max) external onlyOwner {
        _setMaxSwap(max);
    }

    function setOmniXFund(address omnifund_) external onlyOwner {
        _setFund(omnifund_);
    }

    function setPctCut(uint256 pct) external onlyOwner {
        _setPctCut(pct);
    }

    function _setPctCut(uint256 pct) internal {
        require(pct < PCT_CUT_DENOM, "OmniExchange: over pct cut");
        pctCut = pct;
        emit PctCutSet(pct);
    }

    function _setMaxSwap(uint256 max) internal {
        require(max > 0, "OmniExchange: zero max");
        maxSwap = max;
        emit MaxSwapSet(max);
    }

    function _setFund(address fund_) internal {
        require(fund_ != address(0), "OmniExchange: zero address");
        fund = fund_;
        emit FundSet(fund_);
    }
}
