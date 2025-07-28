branch opt_snpshotkv_use_journalEntry
```
cd evm/ex/vm/store/snapshotmulti
go test -bench=.
goos: darwin
goarch: arm64
pkg: github.com/cosmos/evm/x/vm/store/snapshotmulti
cpu: Apple M4 Pro
BenchmarkSequentialCacheMultiStore-14                156           7132378 ns/op         8420955 B/op     118012 allocs/op
PASS
ok      github.com/cosmos/evm/x/vm/store/snapshotmulti  7.509s
```

branch main
```
goos: darwin
goarch: arm64
pkg: github.com/cosmos/evm/x/vm/store/snapshotmulti
cpu: Apple M4 Pro
BenchmarkSequentialCacheMultiStore-14                 67          21176009 ns/op        35912542 B/op     552959 allocs/op
PASS
ok      github.com/cosmos/evm/x/vm/store/snapshotmulti  5.599s
```