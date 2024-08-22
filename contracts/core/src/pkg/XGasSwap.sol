// SPDX-License-Identifier: GPL-3.0-only
pragma solidity ^0.8.12;

import { OmniGasExchange } from "../token/OmniGasExchange.sol";

/**
 * @title XGasSwap
 * @notice Abstract contract that makes it easy to swap ETH for OMNI on Omni.
 */
abstract contract XGasSwap {
    event Refunded(address indexed recipient, uint256 amtETH, string reason);
    event FundedOMNI(address indexed recipient, uint256 ethPaid, uint256 omniReceived);

    OmniGasExchange public immutable OmniGasEx;

    constructor(address exchange) {
        OmniGasEx = OmniGasExchange(exchange);
    }

    /**
     * @notice Swap `amtETH` ETH for OMNI on Omni, funding `recipient`.
     *         Reverts if `amtETH` does not cover xcall fee, or is > max allowed swap.
     */
    function swapForOMNI(address recipient, uint256 amtETH) internal {
        _swap(recipient, amtETH);
    }

    /**
     * @notice Fund `recipient` with `amtETH` worth of OMNI on Omni.
     *         If `amtETH` is not swappable, refund to `recipient`.
     */
    function swapForOMNIOrRefund(address recipient, uint256 amtETH) internal {
        _swapOrRefund(recipient, recipient, amtETH);
    }

    /**
     * @notice Fund `recipient` with `amtETH` worth of OMNI on Omni.
     *         If `amtETH` is not swappable, refund to `refundTo`.
     */
    function swapForOMNIOrRefund(address refundTo, address recipient, uint256 amtETH) internal {
        _swapOrRefund(refundTo, recipient, amtETH);
    }

    function _swapOrRefund(address refundTo, address recipient, uint256 amtETH) internal {
        (, bool succes, string memory reason) = OmniGasEx.simSwap(amtETH);

        if (!succes) {
            emit Refunded(refundTo, amtETH, reason);
            payable(refundTo).transfer(amtETH);
            return;
        }

        _swap(recipient, amtETH);
    }

    function _swap(address recipient, uint256 amtETH) private {
        uint256 amtOMNI = OmniGasEx.swap{ value: amtETH }(recipient);
        emit FundedOMNI(recipient, amtETH, amtOMNI);
    }
}
