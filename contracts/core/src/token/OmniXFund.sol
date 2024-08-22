// SPDX-License-Identifier: GPL-3.0-only
pragma solidity 0.8.24;

import { PausableUpgradeable } from "@openzeppelin/contracts-upgradeable/utils/PausableUpgradeable.sol";
import { OwnableUpgradeable } from "@openzeppelin/contracts-upgradeable/access/OwnableUpgradeable.sol";
import { XAppUpgradeable } from "src/pkg/XAppUpgradeable.sol";
import { ConfLevel } from "src/libraries/ConfLevel.sol";

/**
 * @title OmniXFund
 * @notice A cross-chain fund, allows authorized contracts on other chains to withdraw funds.
 */
contract OmniXFund is XAppUpgradeable, OwnableUpgradeable, PausableUpgradeable {
    /**
     * @notice Map chainID to addr to true, if authorized to withdraw
     */
    mapping(uint64 => mapping(address => bool)) public authed;

    /**
     * @notice Map address to chainID to total funded
     */
    mapping(address => mapping(uint64 => uint256)) public funded;

    constructor() {
        _disableInitializers();
    }

    function initialize(address portal, address owner) external initializer {
        __XApp_init(portal, ConfLevel.Finalized);
        __Ownable_init(owner);
    }

    /**
     * @notice Try to withdraw remaining funds owned to `to`.
     *         The amount owed is `total - funded[to][xmsg.sourceChainId]`.
     * @param recipient     Address to receive the funds
     * @param total         Total (historical) amount requested for `recipient`
     */
    function tryWithdrawRemaining(address recipient, uint256 total) external xrecv whenPaused {
        require(isXCall() && authed[xmsg.sourceChainId][xmsg.sender], "OmniFund: unauthorized");

        // Check we've not already funded total requested
        require(total >= funded[recipient][xmsg.sourceChainId], "OmniFund: already funded");

        uint256 amt = total - funded[recipient][xmsg.sourceChainId];
        (bool success,) = recipient.call{ value: amt }("");

        // Only update funded if the transfer was successful. This allows the user to retry if the transfer fails.
        // A transer may fail if this fund runs out of OMNI, of if recipient.call() runs out of gas.
        //
        // If recipient.call{value: amt}() runs out of gas, the user can retry with a higher xcall gas limit.
        //
        // If recipient.call{value: amt}() reverts due to contract logic, that must be fixed by upgrading
        // the recipient contract. If the recipient contract is not upgradable, those funds are lost.
        // We can add a refund mechanism for this case if it becomes a problem.
        if (success) funded[recipient][xmsg.sourceChainId] += amt;
    }

    function authorize(uint64 chainID, address addr) external onlyOwner {
        authed[chainID][addr] = true;
    }

    function pause() external onlyOwner {
        _pause();
    }

    function unpause() external onlyOwner {
        _unpause();
    }

    receive() external payable { }
}
