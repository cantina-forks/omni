// SPDX-License-Identifier: Unlicense
pragma solidity 0.8.24;

import { XApp } from "src/pkg/XApp.sol";
import { XGasSwap } from "src/pkg/XGasSwap.sol";
import { ConfLevel } from "src/libraries/ConfLevel.sol";

/**
 * @title Funder
 * @notice Example contract that shows how to use XGasPump
 */
contract Funder is XApp, XGasSwap {
    address public thingDoer;

    constructor(address portal, address omniGasEx) XApp(portal, ConfLevel.Latest) XGasSwap(omniGasEx) { }

    /**
     * @notice Simple external method to let msg.sender swap msg.value ETH for OMNI, on Omni
     */
    function swapForOMNI() external payable {
        swapForOMNI(msg.sender, msg.value);
    }

    /**
     * @notice Example of doing an xcall, and using excess msg.value to fund the caller on Omni,
     *        if they paid enough
     */
    function doThingAndMaybeFundMeOMNI() external payable {
        uint256 fee = xcall({
            destChainId: omniChainId(),
            to: thingDoer,
            data: abi.encodeWithSignature("doThing()"),
            gasLimit: 100_000
        });

        require(msg.value >= fee, "Funder: insufficient fee");

        if (msg.value > fee) swapForOMNIOrRefund(msg.sender, msg.value - fee);
    }

    function doThingFee() external view returns (uint256) {
        return feeFor({ destChainId: omniChainId(), data: abi.encodeWithSignature("doThing()"), gasLimit: 100_000 });
    }
}
