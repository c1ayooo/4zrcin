module github.com/c1ayooo/4zrcin

go 1.26.0

require github.com/chainreactors/cyber v0.0.0

require github.com/chainreactors/cyber/aop v0.0.0

require google.golang.org/protobuf v1.36.12

// The Hub is embedded in-process by the inprochost build (-tags inprochost,
// docs/04 M1). The fork is not published under this module path (the repository
// is c1ayooo/cyber-harness), so the single binary always builds from the local
// checkout — the same source the gate reads for the AOP drift check. Builds
// without the tag never import the module, so the replace stays inert there.
replace github.com/chainreactors/cyber => ../cyber-harness

replace github.com/chainreactors/cyber/aop => ../cyber-harness/aop

require (
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/jackc/pgx/v5 v5.13.0
	golang.org/x/crypto v0.59.0
	golang.org/x/sys v0.50.0
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.60.1
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.3 // indirect
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51 // indirect
	github.com/miekg/dns v1.1.72 // indirect
	github.com/projectdiscovery/gologger v1.1.76 // indirect
	github.com/projectdiscovery/retryablehttp-go v1.3.35 // indirect
	github.com/riobard/go-bloom v0.0.0-20200614022211-cdc8013cb5b3 // indirect
	github.com/shadowsocks/go-shadowsocks2 v0.1.5 // indirect
	github.com/tjfoc/gmsm v1.4.1 // indirect
	golang.org/x/mod v0.42.0 // indirect
	golang.org/x/tools v0.52.0 // indirect
)

require (
	connectrpc.com/connect v1.21.1 // indirect
	dario.cat/mergo v1.0.2 // indirect
	github.com/Knetic/govaluate v3.0.1-0.20171022003610-9aa49832a739+incompatible // indirect
	github.com/alecthomas/chroma/v2 v2.27.0 // indirect
	github.com/andybalholm/brotli v1.2.7 // indirect
	github.com/antchfx/htmlquery v1.3.6 // indirect
	github.com/antchfx/xmlquery v1.5.1 // indirect
	github.com/antchfx/xpath v1.3.6 // indirect
	github.com/anytls/sing-anytls v0.0.11 // indirect
	github.com/apernet/hysteria/core/v2 v2.9.2 // indirect
	github.com/apernet/quic-go v0.59.1-0.20260425001925-6c6cc9bcb716 // indirect
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/aymanbagabas/go-pty v0.2.3 // indirect
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/buger/jsonparser v1.1.2 // indirect
	github.com/c1ayooo/4zwebshell v0.0.0
	github.com/chainreactors/fingers v1.2.2-0.20260714063144-070758342f45 // indirect
	github.com/chainreactors/ioa v0.1.2-0.20261114095210-c8126691b2f2 // indirect
	github.com/chainreactors/logs v0.0.0-20260624034259-9aaea4aa52cc // indirect
	github.com/chainreactors/neutron v0.1.1-0.20260714062907-716c6b167cb6 // indirect
	github.com/chainreactors/neutron/operators/full v0.1.1-0.20260704194031-f57d0a560e32 // indirect
	github.com/chainreactors/proton v0.3.3-0.20260707162538-471f99ea6131 // indirect
	github.com/chainreactors/proxyclient v1.1.1-0.20260728110701-74504679dc47 // indirect
	github.com/chainreactors/proxyclient/extra v0.0.0-20260527160727-36cf133952c3 // indirect
	github.com/chainreactors/rem v0.0.0
	github.com/chainreactors/sdk v0.3.4-0.20260708104745-dcad8620f5e9 // indirect
	github.com/chainreactors/utils v0.0.0-20260711153742-f3d210a5fa9d // indirect
	github.com/chainreactors/utils/cert v0.0.0-20260722180147-5b1816060721 // indirect
	github.com/chainreactors/utils/mitmproxy v0.0.0-20261002195803-fc9e07c4b4fd // indirect
	github.com/chainreactors/utils/parsers v0.0.3 // indirect
	github.com/chainreactors/utils/proc v0.0.0-20261002195803-fc9e07c4b4fd // indirect
	github.com/chainreactors/words v0.0.0-20260520145736-270600e60fb4 // indirect
	github.com/charlievieth/fastwalk v1.0.14 // indirect
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/glamour v1.0.0 // indirect
	github.com/charmbracelet/lipgloss v1.1.1-0.20250404203927-76690c660834 // indirect
	github.com/charmbracelet/x/ansi v0.11.7 // indirect
	github.com/charmbracelet/x/cellbuf v0.0.15 // indirect
	github.com/charmbracelet/x/exp/slice v0.0.0-20250327172914-2fdc97757edf // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/cloudflare/circl v1.6.3 // indirect
	github.com/creack/pty v1.1.24 // indirect
	github.com/dlclark/regexp2 v1.12.0 // indirect
	github.com/dlclark/regexp2/v2 v2.5.2 // indirect
	github.com/dop251/goja v0.0.0-20260930195847-0f92c903ca4a // indirect
	github.com/dsnet/compress v0.0.2-0.20230904184137-39efe44ab707 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/edsrzf/mmap-go v1.2.0 // indirect
	github.com/facebookincubator/nvdtools v0.1.5 // indirect
	github.com/go-dedup/megophone v0.0.0-20170830025436-f01be21026f5 // indirect
	github.com/go-dedup/simhash v0.0.0-20170904020510-9ecaca7b509c // indirect
	github.com/go-dedup/text v0.0.0-20170907015346-8bb1b95e3cb7 // indirect
	github.com/go-sourcemap/sourcemap v2.1.3+incompatible // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/gobwas/glob v0.2.3 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/gofrs/uuid/v5 v5.3.2 // indirect
	github.com/golang/groupcache v0.0.0-20241129210726-2c02b8208cf8 // indirect
	github.com/golang/snappy v1.0.0 // indirect
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/google/pprof v0.0.0-20260802141513-ef3492d7dac3 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gookit/config/v2 v2.2.9 // indirect
	github.com/gookit/goutil v0.8.0 // indirect
	github.com/gorilla/css v1.0.1 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/h2non/filetype v1.1.3 // indirect
	github.com/hashicorp/go-version v1.9.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/invopop/jsonschema v0.14.0 // indirect
	github.com/itchyny/gojq v0.12.19 // indirect
	github.com/itchyny/timefmt-go v0.1.8 // indirect
	github.com/jessevdk/go-flags v1.6.1 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/klauspost/compress v1.20.2 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.0 // indirect
	github.com/mark3labs/mcp-go v1.3.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/mattn/go-runewidth v0.0.31 // indirect
	github.com/metacubex/utls v1.7.3 // indirect
	github.com/mholt/archiver v3.1.1+incompatible // indirect
	github.com/microcosm-cc/bluemonday v1.0.27 // indirect
	github.com/mozillazg/go-pinyin v0.21.0 // indirect
	github.com/muesli/reflow v0.3.0 // indirect
	github.com/muesli/termenv v0.16.0 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/nwaples/rardecode v1.1.3 // indirect
	github.com/pb33f/ordered-map/v2 v2.3.1 // indirect
	github.com/pierrec/lz4 v2.6.1+incompatible // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/projectdiscovery/dsl v0.9.4 // indirect
	github.com/projectdiscovery/utils v0.11.15 // indirect
	github.com/puzpuzpuz/xsync/v3 v3.5.1 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/sagernet/sing v0.7.6 // indirect
	github.com/sagernet/sing-vmess v0.2.7 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3 // indirect
	github.com/satori/go.uuid v1.2.0 // indirect
	github.com/sirupsen/logrus v1.9.4 // indirect
	github.com/spaolacci/murmur3 v1.1.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/spf13/cobra v1.10.2 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/stretchr/objx v0.5.3 // indirect
	github.com/stretchr/testify v1.12.1 // indirect
	github.com/tetratelabs/wazero v1.12.0 // indirect
	github.com/tmthrgd/go-hex v0.0.0-20190904060850-447a3041c3bc // indirect
	github.com/twmb/murmur3 v1.1.8 // indirect
	github.com/u-root/u-root v0.16.0 // indirect
	github.com/ulikunitz/xz v0.5.15 // indirect
	github.com/uptrace/bun v1.3.0 // indirect
	github.com/uptrace/bun/dialect/sqlitedialect v1.3.0 // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	github.com/wasilibs/go-re2 v1.11.0 // indirect
	github.com/wasilibs/wazero-helpers v0.0.0-20250123031827-cd30c44769bb // indirect
	github.com/xi2/xz v0.0.0-20171230120015-48954b6210f8 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	github.com/yuin/goldmark v1.8.6 // indirect
	github.com/yuin/goldmark-emoji v1.0.6 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	go.yaml.in/yaml/v4 v4.0.0-rc.2 // indirect
	golang.org/x/exp v0.0.0-20260529124908-c761662dc8c9 // indirect
	golang.org/x/net v0.62.0
	golang.org/x/sync v0.25.0 // indirect
	golang.org/x/term v0.48.0 // indirect
	golang.org/x/text v0.44.0 // indirect
	modernc.org/libc v1.77.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
	mvdan.cc/sh/v3 v3.14.1 // indirect
	sigs.k8s.io/yaml v1.6.0 // indirect
)

replace github.com/chainreactors/rem => ../rem

replace github.com/c1ayooo/4zwebshell => ../4zwebshell
