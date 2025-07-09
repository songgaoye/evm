package ante_test

import (
	"math/big"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/evmos/ethermint/ante/interfaces"
	"github.com/evmos/ethermint/tests"
	evmtypes "github.com/evmos/ethermint/x/evm/types"
)

func (suite *AnteTestSuite) TestEthSetupContextDecorator() {
	tx := evmtypes.NewTxContract(suite.app.EvmKeeper.ChainID(), 1, big.NewInt(10), 1000, big.NewInt(1), nil, nil, nil, nil)

	testCases := []struct {
		name    string
		tx      sdk.Tx
		expPass bool
	}{
		{
			"success - transaction implement GasTx",
			tx,
			true,
		},
	}

	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			ctx, err := interfaces.SetupEthContext(suite.ctx)

			if tc.expPass {
				suite.Require().NoError(err)
				suite.Equal(storetypes.GasConfig{}, ctx.KVGasConfig())
				suite.Equal(storetypes.GasConfig{}, ctx.TransientKVGasConfig())
			} else {
				suite.Require().Error(err)
			}
		})
	}
}

func (suite *AnteTestSuite) TestValidateBasicDecorator() {
	addr, privKey := tests.NewAddrKey()

	signedTx := evmtypes.NewTxContract(suite.app.EvmKeeper.ChainID(), 1, big.NewInt(10), 1000, big.NewInt(1), nil, nil, nil, nil)
	signedTx.From = addr.Bytes()
	err := signedTx.Sign(suite.ethSigner, tests.NewSigner(privKey))
	suite.Require().NoError(err)

	unprotectedTx := evmtypes.NewTxContract(nil, 1, big.NewInt(10), 1000, big.NewInt(1), nil, nil, nil, nil)
	unprotectedTx.From = addr.Bytes()
	err = unprotectedTx.Sign(ethtypes.HomesteadSigner{}, tests.NewSigner(privKey))
	suite.Require().NoError(err)
	tmTx, err := unprotectedTx.BuildTx(suite.clientCtx.TxConfig.NewTxBuilder(), evmtypes.DefaultEVMDenom)
	suite.Require().NoError(err)

	testCases := []struct {
		name                string
		tx                  sdk.Tx
		allowUnprotectedTxs bool
		reCheckTx           bool
		expPass             bool
	}{
		{"invalid transaction type", &invalidTx{}, false, false, false},
		{
			"invalid sender",
			evmtypes.NewTx(suite.app.EvmKeeper.ChainID(), 1, &addr, big.NewInt(10), 1000, big.NewInt(1), nil, nil, nil, nil),
			true,
			false,
			false,
		},
		{"invalid, reject unprotected txs", tmTx, false, false, false},
		{"successful, allow unprotected txs", tmTx, true, false, true},
	}

	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			suite.evmParamsOption = func(params *evmtypes.Params) {
				params.AllowUnprotectedTxs = tc.allowUnprotectedTxs
			}
			suite.SetupTest()

			evmParams := suite.app.EvmKeeper.GetParams(suite.ctx)
			chainID := suite.app.EvmKeeper.ChainID()
			chainCfg := evmParams.GetChainConfig()
			ethCfg := chainCfg.EthereumConfig(chainID)
			baseFee := suite.app.EvmKeeper.GetBaseFee(suite.ctx, ethCfg)

			err := interfaces.ValidateEthBasic(suite.ctx.WithIsReCheckTx(tc.reCheckTx), tc.tx, &evmParams, baseFee)

			if tc.expPass {
				suite.Require().NoError(err)
			} else {
				suite.Require().Error(err)
			}
		})
	}
	suite.evmParamsOption = nil
}
