package erc20

import (
	"math/big"

	"github.com/ethereum/go-ethereum/core/vm"

	"github.com/cosmos/evm/precompiles/erc20"
	"github.com/cosmos/evm/precompiles/testutil"
	erc20types "github.com/cosmos/evm/x/erc20/types"
	"github.com/cosmos/evm/x/vm/statedb"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

var (
	tokenDenom = "xmpl"
	// XMPLCoin is a dummy coin used for testing purposes.
	XMPLCoin = sdk.NewCoins(sdk.NewInt64Coin(tokenDenom, 1e18))
	// toAddr is a dummy address used for testing purposes.
	toAddr = GenerateAddress()
)

func (s *PrecompileTestSuite) TestTransfer() {
	method := s.precompile.Methods[erc20.TransferMethod]
	// fromAddr is the address of the keyring account used for testing.
	fromAddr := s.keyring.GetKey(0).Addr
	testcases := []struct {
		name        string
		malleate    func() []interface{}
		postCheck   func()
		expErr      bool
		errContains string
	}{
		{
			"fail - negative amount",
			func() []interface{} {
				return []interface{}{toAddr, big.NewInt(-1)}
			},
			func() {},
			true,
			"coin -1xmpl amount is not positive",
		},
		{
			"fail - invalid to address",
			func() []interface{} {
				return []interface{}{"", big.NewInt(100)}
			},
			func() {},
			true,
			"invalid to address",
		},
		{
			"fail - invalid amount",
			func() []interface{} {
				return []interface{}{toAddr, ""}
			},
			func() {},
			true,
			"invalid amount",
		},
		{
			"fail - not enough balance",
			func() []interface{} {
				return []interface{}{toAddr, big.NewInt(2e18)}
			},
			func() {},
			true,
			erc20.ErrTransferAmountExceedsBalance.Error(),
		},
		{
			"pass",
			func() []interface{} {
				return []interface{}{toAddr, big.NewInt(100)}
			},
			func() {
				toAddrBalance := s.network.App.GetBankKeeper().GetBalance(s.network.GetContext(), toAddr.Bytes(), tokenDenom)
				s.Require().Equal(big.NewInt(100), toAddrBalance.Amount.BigInt(), "expected toAddr to have 100 XMPL")
			},
			false,
			"",
		},
	}

	for _, tc := range testcases {
		s.Run(tc.name, func() {
			s.SetupTest()
			stateDB := s.network.GetStateDB()

			var contract *vm.Contract
			contract, ctx := testutil.NewPrecompileContract(s.T(), s.network.GetContext(), fromAddr, s.precompile.Address(), 0)

			// Mint some coins to the module account and then send to the from address
			err := s.network.App.GetBankKeeper().MintCoins(s.network.GetContext(), erc20types.ModuleName, XMPLCoin)
			s.Require().NoError(err, "failed to mint coins")
			err = s.network.App.GetBankKeeper().SendCoinsFromModuleToAccount(s.network.GetContext(), erc20types.ModuleName, fromAddr.Bytes(), XMPLCoin)
			s.Require().NoError(err, "failed to send coins from module to account")

			_, err = s.precompile.Transfer(ctx, contract, stateDB, &method, tc.malleate())
			if tc.expErr {
				s.Require().Error(err, "expected transfer transaction to fail")
				s.Require().Contains(err.Error(), tc.errContains, "expected transfer transaction to fail with specific error")
			} else {
				s.Require().NoError(err, "expected transfer transaction succeeded")
				tc.postCheck()
			}
		})
	}
}

func (s *PrecompileTestSuite) TestTransferFrom() {
	var (
		ctx  sdk.Context
		stDB *statedb.StateDB
	)
	method := s.precompile.Methods[erc20.TransferFromMethod]
	// owner of the tokens
	owner := s.keyring.GetKey(0)
	// spender of the tokens
	spender := s.keyring.GetKey(1)

	testcases := []struct {
		name        string
		malleate    func() []interface{}
		postCheck   func()
		expErr      bool
		errContains string
	}{
		{
			"fail - negative amount",
			func() []interface{} {
				return []interface{}{owner.Addr, toAddr, big.NewInt(-1)}
			},
			func() {},
			true,
			"coin -1xmpl amount is not positive",
		},
		{
			"fail - invalid from address",
			func() []interface{} {
				return []interface{}{"", toAddr, big.NewInt(100)}
			},
			func() {},
			true,
			"invalid from address",
		},
		{
			"fail - invalid to address",
			func() []interface{} {
				return []interface{}{owner.Addr, "", big.NewInt(100)}
			},
			func() {},
			true,
			"invalid to address",
		},
		{
			"fail - invalid amount",
			func() []interface{} {
				return []interface{}{owner.Addr, toAddr, ""}
			},
			func() {},
			true,
			"invalid amount",
		},
		{
			"fail - not enough allowance",
			func() []interface{} {
				return []interface{}{owner.Addr, toAddr, big.NewInt(100)}
			},
			func() {},
			true,
			erc20.ErrInsufficientAllowance.Error(),
		},
		{
			"fail - not enough balance",
			func() []interface{} {
				err := s.network.App.GetErc20Keeper().SetAllowance(ctx, s.precompile.Address(), owner.Addr, spender.Addr, big.NewInt(5e18))
				s.Require().NoError(err, "failed to set allowance")

				return []interface{}{owner.Addr, toAddr, big.NewInt(2e18)}
			},
			func() {},
			true,
			erc20.ErrTransferAmountExceedsBalance.Error(),
		},
		{
			"fail - spend on behalf of own account without allowance",
			func() []interface{} {
				// Mint some coins to the module account and then send to the spender address
				err := s.network.App.GetBankKeeper().MintCoins(ctx, erc20types.ModuleName, XMPLCoin)
				s.Require().NoError(err, "failed to mint coins")
				err = s.network.App.GetBankKeeper().SendCoinsFromModuleToAccount(ctx, erc20types.ModuleName, spender.AccAddr, XMPLCoin)
				s.Require().NoError(err, "failed to send coins from module to account")

				// NOTE: no allowance is necessary to spend on behalf of the same account
				return []interface{}{spender.Addr, toAddr, big.NewInt(100)}
			},
			func() {
				toAddrBalance := s.network.App.GetBankKeeper().GetBalance(ctx, toAddr.Bytes(), tokenDenom)
				s.Require().Equal(big.NewInt(100), toAddrBalance.Amount.BigInt(), "expected toAddr to have 100 XMPL")
			},
			true,
			"",
		},
		{
			"pass - spend on behalf of own account with allowance",
			func() []interface{} {
				// Mint some coins to the module account and then send to the spender address
				err := s.network.App.GetBankKeeper().MintCoins(ctx, erc20types.ModuleName, XMPLCoin)
				s.Require().NoError(err, "failed to mint coins")
				err = s.network.App.GetBankKeeper().SendCoinsFromModuleToAccount(ctx, erc20types.ModuleName, spender.AccAddr, XMPLCoin)
				s.Require().NoError(err, "failed to send coins from module to account")

				err = s.network.App.GetErc20Keeper().SetAllowance(ctx, s.precompile.Address(), spender.Addr, spender.Addr, big.NewInt(100))
				s.Require().NoError(err, "failed to set allowance")

				// NOTE: no allowance is necessary to spend on behalf of the same account
				return []interface{}{spender.Addr, toAddr, big.NewInt(100)}
			},
			func() {
				toAddrBalance := s.network.App.GetBankKeeper().GetBalance(ctx, toAddr.Bytes(), tokenDenom)
				s.Require().Equal(big.NewInt(100), toAddrBalance.Amount.BigInt(), "expected toAddr to have 100 XMPL")
			},
			false,
			"",
		},
		{
			"pass - spend on behalf of other account",
			func() []interface{} {
				err := s.network.App.GetErc20Keeper().SetAllowance(ctx, s.precompile.Address(), owner.Addr, spender.Addr, big.NewInt(300))
				s.Require().NoError(err, "failed to set allowance")

				return []interface{}{owner.Addr, toAddr, big.NewInt(100)}
			},
			func() {
				toAddrBalance := s.network.App.GetBankKeeper().GetBalance(ctx, toAddr.Bytes(), tokenDenom)
				s.Require().Equal(big.NewInt(100), toAddrBalance.Amount.BigInt(), "expected toAddr to have 100 XMPL")
			},
			false,
			"",
		},
	}

	for _, tc := range testcases {
		s.Run(tc.name, func() {
			s.SetupTest()
			ctx = s.network.GetContext()
			stDB = s.network.GetStateDB()

			var contract *vm.Contract
			contract, ctx = testutil.NewPrecompileContract(s.T(), ctx, spender.Addr, s.precompile.Address(), 0)

			// Mint some coins to the module account and then send to the from address
			err := s.network.App.GetBankKeeper().MintCoins(ctx, erc20types.ModuleName, XMPLCoin)
			s.Require().NoError(err, "failed to mint coins")
			err = s.network.App.GetBankKeeper().SendCoinsFromModuleToAccount(ctx, erc20types.ModuleName, owner.AccAddr, XMPLCoin)
			s.Require().NoError(err, "failed to send coins from module to account")

			_, err = s.precompile.TransferFrom(ctx, contract, stDB, &method, tc.malleate())
			if tc.expErr {
				s.Require().Error(err, "expected transfer transaction to fail")
				s.Require().Contains(err.Error(), tc.errContains, "expected transfer transaction to fail with specific error")
			} else {
				s.Require().NoError(err, "expected transfer transaction succeeded")
				tc.postCheck()
			}
		})
	}
}
