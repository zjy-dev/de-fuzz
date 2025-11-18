# Benchmark Test Report

## Test Environment
- **Commit ID**: 6d508c61f6d3f1b2a94ee34c16aebaa1f1efacad
- **Commit Short**: 6d508c6
- **Test Time**: 2025-11-19 02:56:19 CST
- **CPU**: Intel(R) Core(TM) Ultra 9 275HX
- **CPU Cores**: 24
- **Memory**: 15Gi

## Benchmark Results

```
goos: linux
goarch: amd64
pkg: github.com/zjy-dev/de-fuzz/internal/coverage
cpu: Intel(R) Core(TM) Ultra 9 275HX
BenchmarkCppAbstractor_ShortFile-24                         	    7528	    474064 ns/op	   57930 B/op	    1744 allocs/op
BenchmarkCppAbstractor_LongFile_1Function4Lines-24          	     296	  11812745 ns/op	  497314 B/op	   14697 allocs/op
BenchmarkCppAbstractor_LongFile_10Functions100Lines-24      	     100	  31023641 ns/op	 3675050 B/op	  161780 allocs/op
BenchmarkCppAbstractor_LongFile_1Function50Lines-24         	     303	  11965766 ns/op	  499434 B/op	   14704 allocs/op
BenchmarkCppAbstractor_LongFile_5Functions20LinesEach-24    	     150	  24203908 ns/op	 2478113 B/op	  106768 allocs/op
BenchmarkCppAbstractor_MultiFile-24                         	     230	  15618697 ns/op	 1051444 B/op	   39594 allocs/op
BenchmarkAbstractorRegistry_AbstractAll-24                  	     290	  12641709 ns/op	  555697 B/op	   16445 allocs/op
BenchmarkCppAbstractor_ParsingOnly-24                       	     300	  12044147 ns/op	  497312 B/op	   14697 allocs/op
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/coverage	37.272s
```
