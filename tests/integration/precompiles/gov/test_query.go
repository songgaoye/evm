package gov

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"

	cmn "github.com/cosmos/evm/precompiles/common"
	"github.com/cosmos/evm/precompiles/gov"
	"github.com/cosmos/evm/precompiles/testutil"
	testconstants "github.com/cosmos/evm/testutil/constants"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/testutil/testdata"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
)

var (
	_, _, addr = testdata.KeyTestPubAddr()
	// gov account authority address
	govAcct = authtypes.NewModuleAddress(govtypes.ModuleName)
	// TestProposalMsgs are msgs used on a proposal.
	TestProposalMsgs = []sdk.Msg{
		banktypes.NewMsgSend(govAcct, addr, sdk.NewCoins(sdk.NewCoin(testconstants.ExampleAttoDenom, math.NewInt(1000)))),
	}
)

func (s *PrecompileTestSuite) TestGetVotes() {
	var ctx sdk.Context
	method := s.precompile.Methods[gov.GetVotesMethod]
	gas := uint64(200_000)
	testCases := []struct {
		name        string
		malleate    func() []gov.WeightedVote
		args        []interface{}
		expPass     bool
		errContains string
		expTotal    uint64
	}{
		{
			name: "valid query",
			malleate: func() []gov.WeightedVote {
				proposalID := uint64(1)
				voter := s.keyring.GetAccAddr(0)
				voteOption := &govv1.WeightedVoteOption{
					Option: govv1.OptionYes,
					Weight: "1.0",
				}

				err := s.network.App.GetGovKeeper().AddVote(
					s.network.GetContext(),
					proposalID,
					voter,
					[]*govv1.WeightedVoteOption{voteOption},
					"",
				)
				s.Require().NoError(err)

				return []gov.WeightedVote{{
					ProposalId: proposalID,
					Voter:      s.keyring.GetAddr(0),
					Options: []gov.WeightedVoteOption{
						{
							Option: uint8(voteOption.Option), //nolint:gosec // G115 -- integer overflow is not happening here
							Weight: voteOption.Weight,
						},
					},
				}}
			},
			args:     []interface{}{uint64(1), query.PageRequest{Limit: 10, CountTotal: true}},
			expPass:  true,
			expTotal: 1,
		},
		{
			name:        "invalid proposal ID",
			args:        []interface{}{uint64(0), query.PageRequest{Limit: 10, CountTotal: true}},
			expPass:     false,
			errContains: "proposal id can not be 0",
		},
		{
			name:        "fail - invalid number of args",
			args:        []interface{}{},
			errContains: fmt.Sprintf(cmn.ErrInvalidNumberOfArgs, 2, 0),
		},
		{
			name:        "fail - invalid arg types",
			args:        []interface{}{"string argument 1", 2},
			errContains: "error while unpacking args to VotesInput",
		},
	}

	for _, tc := range testCases {
		s.Run(tc.name, func() {
			s.SetupTest()
			ctx = s.network.GetContext()

			var votes []gov.WeightedVote
			if tc.malleate != nil {
				votes = tc.malleate()
			}

			var contract *vm.Contract
			contract, ctx = testutil.NewPrecompileContract(s.T(), ctx, s.keyring.GetAddr(0), s.precompile.Address(), gas)

			bz, err := s.precompile.GetVotes(ctx, &method, contract, tc.args)

			if tc.expPass {
				var out gov.VotesOutput
				err = s.precompile.UnpackIntoInterface(&out, gov.GetVotesMethod, bz)
				s.Require().NoError(err)
				s.Require().Equal(votes, out.Votes)
				s.Require().Equal(tc.expTotal, out.PageResponse.Total)
			} else {
				s.Require().Error(err)
				s.Require().ErrorContains(err, tc.errContains)
			}
		})
	}
}

func (s *PrecompileTestSuite) TestGetVote() {
	var voter sdk.AccAddress
	var voterAddr common.Address

	method := s.precompile.Methods[gov.GetVoteMethod]

	testCases := []struct {
		name          string
		malleate      func() []interface{}
		expPass       bool
		expPropNumber uint64
		expVoter      common.Address
		errContains   string
	}{
		{
			name: "valid query",
			malleate: func() []interface{} {
				err := s.network.App.GetGovKeeper().AddVote(s.network.GetContext(), 1, voter, []*govv1.WeightedVoteOption{{Option: govv1.OptionYes, Weight: "1.0"}}, "")
				s.Require().NoError(err)

				return []interface{}{uint64(1), voterAddr}
			},
			expPropNumber: uint64(1),
			expVoter:      common.BytesToAddress(voter.Bytes()),
			expPass:       true,
		},
		{
			name:    "invalid proposal ID",
			expPass: false,
			malleate: func() []interface{} {
				err := s.network.App.GetGovKeeper().AddVote(s.network.GetContext(), 1, voter, []*govv1.WeightedVoteOption{{Option: govv1.OptionYes, Weight: "1.0"}}, "")
				s.Require().NoError(err)

				return []interface{}{uint64(10), voterAddr}
			},
			errContains: "not found for proposal",
		},
		{
			name: "non-existent vote",
			malleate: func() []interface{} {
				return []interface{}{uint64(1), voterAddr}
			},
			expPass:     false,
			errContains: "not found for proposal",
		},
		{
			name: "invalid number of args",
			malleate: func() []interface{} {
				return []interface{}{}
			},
			errContains: fmt.Sprintf(cmn.ErrInvalidNumberOfArgs, 2, 0),
		},
		{
			name: "fail - invalid proposal id",
			malleate: func() []interface{} {
				return []interface{}{"string argument 1", 2}
			},
			errContains: "invalid proposal id",
		},
		{
			name: "fail - invalid voter address",
			malleate: func() []interface{} {
				return []interface{}{uint64(0), 2}
			},
			errContains: "invalid voter address",
		},
	}

	for _, tc := range testCases {
		s.Run(tc.name, func() {
			s.SetupTest()

			voter = s.keyring.GetAccAddr(0)
			voterAddr = s.keyring.GetAddr(0)
			gas := uint64(200_000)

			var args []interface{}
			if tc.malleate != nil {
				args = tc.malleate()
			}

			contract, ctx := testutil.NewPrecompileContract(s.T(), s.network.GetContext(), voterAddr, s.precompile.Address(), gas)

			bz, err := s.precompile.GetVote(ctx, &method, contract, args)

			expVote := gov.WeightedVote{
				ProposalId: tc.expPropNumber,
				Voter:      voterAddr,
				Options:    []gov.WeightedVoteOption{{Option: uint8(govv1.OptionYes), Weight: "1.0"}},
				Metadata:   "",
			}

			if tc.expPass {
				s.Require().NoError(err)

				var out gov.VoteOutput
				err = s.precompile.UnpackIntoInterface(&out, gov.GetVoteMethod, bz)

				s.Require().NoError(err)
				s.Require().Equal(expVote.ProposalId, out.Vote.ProposalId)
				s.Require().Equal(expVote.Voter, out.Vote.Voter)
				s.Require().Equal(expVote.Options, out.Vote.Options)
				s.Require().Equal(expVote.Metadata, out.Vote.Metadata)
			} else {
				s.Require().Error(err)
				s.Require().ErrorContains(err, tc.errContains)
			}
		})
	}
}

func (s *PrecompileTestSuite) TestGetDeposit() {
	var depositor sdk.AccAddress
	method := s.precompile.Methods[gov.GetDepositMethod]
	testCases := []struct {
		name          string
		malleate      func()
		propNumber    uint64
		expPass       bool
		expPropNumber uint64
		gas           uint64
		errContains   string
	}{
		{
			name:          "valid query",
			malleate:      func() {},
			propNumber:    uint64(1),
			expPropNumber: uint64(1),
			expPass:       true,
			gas:           200_000,
		},
		{
			name:        "invalid proposal ID",
			propNumber:  uint64(10),
			expPass:     false,
			gas:         200_000,
			malleate:    func() {},
			errContains: "not found",
		},
	}

	for _, tc := range testCases {
		s.Run(tc.name, func() {
			s.SetupTest()

			depositor = s.keyring.GetAccAddr(0)

			tc.malleate()

			contract, ctx := testutil.NewPrecompileContract(s.T(), s.network.GetContext(), s.keyring.GetAddr(0), s.precompile.Address(), tc.gas)

			args := []interface{}{tc.propNumber, common.BytesToAddress(depositor.Bytes())}
			bz, err := s.precompile.GetDeposit(ctx, &method, contract, args)

			if tc.expPass {
				s.Require().NoError(err)
				var out gov.DepositOutput
				err = s.precompile.UnpackIntoInterface(&out, gov.GetDepositMethod, bz)

				s.Require().NoError(err)
				s.Require().Equal(tc.expPropNumber, out.Deposit.ProposalId)
				s.Require().Equal(common.BytesToAddress(depositor.Bytes()), out.Deposit.Depositor)
				s.Require().Equal([]cmn.Coin{{Denom: "aatom", Amount: big.NewInt(100)}}, out.Deposit.Amount)
			} else {
				s.Require().Error(err)
				s.Require().Contains(err.Error(), tc.errContains)
			}
		})
	}
}

func (s *PrecompileTestSuite) TestGetDeposits() {
	method := s.precompile.Methods[gov.GetDepositsMethod]
	testCases := []struct {
		name     string
		malleate func() []gov.DepositData
		args     []interface{}
		expPass  bool
		expTotal uint64
		gas      uint64
	}{
		{
			name: "valid query",
			malleate: func() []gov.DepositData {
				return []gov.DepositData{
					{ProposalId: 1, Depositor: s.keyring.GetAddr(0), Amount: []cmn.Coin{{Denom: s.network.GetBaseDenom(), Amount: big.NewInt(100)}}},
				}
			},
			args:     []interface{}{uint64(1), query.PageRequest{Limit: 10, CountTotal: true}},
			expPass:  true,
			expTotal: 1,
			gas:      200_000,
		},
		{
			name:    "invalid proposal ID",
			args:    []interface{}{uint64(0), query.PageRequest{Limit: 10, CountTotal: true}},
			expPass: false,
			gas:     200_000,
			malleate: func() []gov.DepositData {
				return []gov.DepositData{}
			},
		},
	}

	for _, tc := range testCases {
		s.Run(tc.name, func() {
			s.SetupTest()
			ctx := s.network.GetContext()

			deposits := tc.malleate()
			contract, ctx := testutil.NewPrecompileContract(s.T(), ctx, s.keyring.GetAddr(0), s.precompile.Address(), tc.gas)

			bz, err := s.precompile.GetDeposits(ctx, &method, contract, tc.args)
			if tc.expPass {
				var out gov.DepositsOutput
				err = s.precompile.UnpackIntoInterface(&out, gov.GetDepositsMethod, bz)
				s.Require().NoError(err)
				s.Require().Equal(deposits, out.Deposits)
				s.Require().Equal(tc.expTotal, out.PageResponse.Total)
			} else {
				s.Require().Error(err)
			}
		})
	}
}

func (s *PrecompileTestSuite) TestGetTallyResult() {
	method := s.precompile.Methods[gov.GetTallyResultMethod]
	testCases := []struct {
		name        string
		malleate    func() (gov.TallyResultData, uint64)
		expPass     bool
		gas         uint64
		errContains string
	}{
		{
			name: "valid query",
			malleate: func() (gov.TallyResultData, uint64) {
				proposal, err := s.network.App.GetGovKeeper().SubmitProposal(s.network.GetContext(), TestProposalMsgs, "", "Proposal", "testing proposal", s.keyring.GetAccAddr(0), false)
				s.Require().NoError(err)
				votingStarted, err := s.network.App.GetGovKeeper().AddDeposit(s.network.GetContext(), proposal.Id, s.keyring.GetAccAddr(0), sdk.NewCoins(sdk.NewCoin(s.network.GetBaseDenom(), math.NewInt(100))))
				s.Require().NoError(err)
				s.Require().True(votingStarted)
				err = s.network.App.GetGovKeeper().AddVote(s.network.GetContext(), proposal.Id, s.keyring.GetAccAddr(0), govv1.NewNonSplitVoteOption(govv1.OptionYes), "")
				s.Require().NoError(err)
				return gov.TallyResultData{
					Yes:        "3000000000000000000",
					Abstain:    "0",
					No:         "0",
					NoWithVeto: "0",
				}, proposal.Id
			},
			expPass: true,
			gas:     200_000,
		},
		{
			name:        "invalid proposal ID",
			expPass:     false,
			gas:         200_000,
			malleate:    func() (gov.TallyResultData, uint64) { return gov.TallyResultData{}, 10 },
			errContains: "proposal 10 doesn't exist",
		},
	}

	for _, tc := range testCases {
		s.Run(tc.name, func() {
			s.SetupTest()

			expTally, propID := tc.malleate()

			contract, ctx := testutil.NewPrecompileContract(s.T(), s.network.GetContext(), s.keyring.GetAddr(0), s.precompile.Address(), tc.gas)

			args := []interface{}{propID}
			bz, err := s.precompile.GetTallyResult(ctx, &method, contract, args)

			if tc.expPass {
				s.Require().NoError(err)
				var out gov.TallyResultOutput
				err = s.precompile.UnpackIntoInterface(&out, gov.GetTallyResultMethod, bz)

				s.Require().NoError(err)
				s.Require().Equal(expTally, out.TallyResult)
			} else {
				s.Require().Error(err)
				s.Require().Contains(err.Error(), tc.errContains)
			}
		})
	}
}

func (s *PrecompileTestSuite) TestGetProposal() {
	method := s.precompile.Methods[gov.GetProposalMethod]

	testCases := []struct {
		name        string
		malleate    func() []interface{}
		postCheck   func(data *gov.ProposalData)
		gas         uint64
		expError    bool
		errContains string
	}{
		{
			"fail - empty input args",
			func() []interface{} {
				return []interface{}{}
			},
			func(_ *gov.ProposalData) {},
			200000,
			true,
			fmt.Sprintf(cmn.ErrInvalidNumberOfArgs, 1, 0),
		},
		{
			"fail - invalid proposal ID",
			func() []interface{} {
				return []interface{}{uint64(0)}
			},
			func(_ *gov.ProposalData) {},
			200000,
			true,
			"proposal id can not be 0",
		},
		{
			"fail - proposal doesn't exist",
			func() []interface{} {
				return []interface{}{uint64(10)}
			},
			func(_ *gov.ProposalData) {},
			200000,
			true,
			"proposal 10 doesn't exist",
		},
		{
			"success - get proposal",
			func() []interface{} {
				return []interface{}{uint64(1)}
			},
			func(data *gov.ProposalData) {
				s.Require().Equal(uint64(1), data.Id)
				s.Require().Equal(uint32(govv1.StatusVotingPeriod), data.Status)
				s.Require().Equal(s.keyring.GetAddr(0), data.Proposer)
				s.Require().Equal("test prop", data.Title)
				s.Require().Equal("test prop", data.Summary)
				s.Require().Equal("ipfs://CID", data.Metadata)
				s.Require().Len(data.Messages, 1)
				s.Require().Equal("/cosmos.bank.v1beta1.MsgSend", data.Messages[0])
			},
			200000,
			false,
			"",
		},
	}

	for _, tc := range testCases {
		s.Run(tc.name, func() {
			s.SetupTest()

			contract, ctx := testutil.NewPrecompileContract(s.T(), s.network.GetContext(), s.keyring.GetAddr(0), s.precompile.Address(), tc.gas)

			bz, err := s.precompile.GetProposal(ctx, &method, contract, tc.malleate())

			if tc.expError {
				s.Require().Error(err)
				s.Require().Contains(err.Error(), tc.errContains)
			} else {
				s.Require().NoError(err)
				var out gov.ProposalOutput
				err = s.precompile.UnpackIntoInterface(&out, gov.GetProposalMethod, bz)
				s.Require().NoError(err)
				tc.postCheck(&out.Proposal)
			}
		})
	}
}

func (s *PrecompileTestSuite) TestGetProposals() {
	method := s.precompile.Methods[gov.GetProposalsMethod]

	testCases := []struct {
		name        string
		malleate    func() []interface{}
		postCheck   func(data []gov.ProposalData, pageRes *query.PageResponse)
		gas         uint64
		expError    bool
		errContains string
	}{
		{
			"fail - empty input args",
			func() []interface{} {
				return []interface{}{}
			},
			func(_ []gov.ProposalData, _ *query.PageResponse) {},
			200000,
			true,
			fmt.Sprintf(cmn.ErrInvalidNumberOfArgs, 4, 0),
		},
		{
			"success - get all proposals",
			func() []interface{} {
				return []interface{}{
					uint32(govv1.StatusNil),
					common.Address{},
					common.Address{},
					query.PageRequest{
						Limit:      10,
						CountTotal: true,
					},
				}
			},
			func(data []gov.ProposalData, pageRes *query.PageResponse) {
				s.Require().Len(data, 2)
				s.Require().Equal(uint64(2), pageRes.Total)

				proposal := data[0]
				s.Require().Equal(uint64(1), proposal.Id)
				s.Require().Equal(uint32(govv1.StatusVotingPeriod), proposal.Status)
				s.Require().Equal(s.keyring.GetAddr(0), proposal.Proposer)
				s.Require().Equal("test prop", proposal.Title)
				s.Require().Equal("test prop", proposal.Summary)
				s.Require().Equal("ipfs://CID", proposal.Metadata)
				s.Require().Len(proposal.Messages, 1)
				s.Require().Equal("/cosmos.bank.v1beta1.MsgSend", proposal.Messages[0])
			},
			200000,
			false,
			"",
		},
		{
			"success - filter by status",
			func() []interface{} {
				return []interface{}{
					uint32(govv1.StatusVotingPeriod),
					common.Address{},
					common.Address{},
					query.PageRequest{
						Limit:      10,
						CountTotal: true,
					},
				}
			},
			func(data []gov.ProposalData, pageRes *query.PageResponse) {
				s.Require().Len(data, 2)
				s.Require().Equal(uint64(2), pageRes.Total)
				s.Require().Equal(uint32(govv1.StatusVotingPeriod), data[0].Status)
				s.Require().Equal(uint32(govv1.StatusVotingPeriod), data[1].Status)
			},
			200000,
			false,
			"",
		},
		{
			"success - filter by voter",
			func() []interface{} {
				// First add a vote
				err := s.network.App.GetGovKeeper().AddVote(s.network.GetContext(), 1, s.keyring.GetAccAddr(0), govv1.NewNonSplitVoteOption(govv1.OptionYes), "")
				s.Require().NoError(err)

				return []interface{}{
					uint32(govv1.StatusVotingPeriod),
					s.keyring.GetAddr(0),
					common.Address{},
					query.PageRequest{
						Limit:      10,
						CountTotal: true,
					},
				}
			},
			func(data []gov.ProposalData, pageRes *query.PageResponse) {
				s.Require().Len(data, 1)
				s.Require().Equal(uint64(1), pageRes.Total)
			},
			200000,
			false,
			"",
		},
		{
			"success - filter by depositor",
			func() []interface{} {
				return []interface{}{
					uint32(govv1.StatusVotingPeriod),
					common.Address{},
					s.keyring.GetAddr(0),
					query.PageRequest{
						Limit:      10,
						CountTotal: true,
					},
				}
			},
			func(data []gov.ProposalData, pageRes *query.PageResponse) {
				s.Require().Len(data, 1)
				s.Require().Equal(uint64(1), pageRes.Total)
			},
			200000,
			false,
			"",
		},
	}

	for _, tc := range testCases {
		s.Run(tc.name, func() {
			s.SetupTest()

			contract, ctx := testutil.NewPrecompileContract(s.T(), s.network.GetContext(), s.keyring.GetAddr(0), s.precompile.Address(), tc.gas)

			bz, err := s.precompile.GetProposals(ctx, &method, contract, tc.malleate())

			if tc.expError {
				s.Require().Error(err)
				s.Require().Contains(err.Error(), tc.errContains)
			} else {
				s.Require().NoError(err)
				var out gov.ProposalsOutput
				err = s.precompile.UnpackIntoInterface(&out, gov.GetProposalsMethod, bz)
				s.Require().NoError(err)
				tc.postCheck(out.Proposals, &out.PageResponse)
			}
		})
	}
}

func (s *PrecompileTestSuite) TestGetParams() {
	testCases := []struct {
		name        string
		malleate    func() []interface{}
		expPass     bool
		errContains string
	}{
		{
			"fail - not empty input args",
			func() []interface{} {
				return []interface{}{""}
			},
			false,
			fmt.Sprintf(cmn.ErrInvalidNumberOfArgs, 0, 1),
		},
		{
			"success - get all params",
			func() []interface{} {
				return []interface{}{}
			},
			true,
			"",
		},
	}

	for _, tc := range testCases {
		s.Run(tc.name, func() {
			s.SetupTest()

			method := s.precompile.Methods[gov.GetParamsMethod]
			_, err := s.precompile.GetParams(s.network.GetContext(), &method, nil, tc.malleate())

			if tc.expPass {
				s.Require().NoError(err)
			} else {
				s.Require().Error(err)
				s.Require().Contains(err.Error(), tc.errContains)
			}
		})
	}
}
