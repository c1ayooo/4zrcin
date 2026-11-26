// Command rem-relay is the public host's half of the platform's reverse
// connection (docs/14 §7.5): a rem server on its loopback whose listener the
// local reverse proxy forwards the public entry to. It is a separate binary
// because the relay host must not need the 4zrcin tree — but it is built from
// this module so the websocket tunnel registration matches the platform's.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	remrunner "github.com/chainreactors/rem/runner"
	_ "github.com/chainreactors/rem/protocol/tunnel/websocket"
	_ "github.com/chainreactors/rem/protocol/wrapper"
)

func main() {
	listen := flag.String("s", "ws://127.0.0.1:18081/relay?wrapper=raw", "rem 服务监听地址（回环，由反代转进来）")
	iface := flag.String("i", "127.0.0.1", "监听绑定的接口")
	key := flag.String("k", "", "登录密钥，与平台 --relay-key 一致")
	quiet := flag.Bool("q", false, "静默日志")
	// The systemd unit may carry --no-sub from the shared template; the relay
	// never opens a subscription server anyway.
	_ = flag.Bool("no-sub", true, "接受但不使用（relay 不开订阅服务）")
	flag.Parse()

	var opt remrunner.Options
	args := []string{"-s", *listen, "-i", *iface, "--no-sub"}
	if *key != "" {
		args = append(args, "-k", *key)
	}
	if *quiet {
		args = append(args, "-q")
	}
	if err := opt.ParseArgs(args); err != nil {
		log.Fatalf("参数：%v", err)
	}
	r, err := opt.Prepare()
	if err != nil {
		log.Fatalf("准备：%v", err)
	}
	r.URLs.ConsoleURL = r.ConsoleURLs[0]
	con, err := remrunner.NewConsole(r, r.URLs)
	if err != nil {
		log.Fatalf("服务：%v", err)
	}
	fmt.Fprintln(os.Stderr, "rem-relay listening")
	if err := con.Run(); err != nil {
		log.Fatalf("运行：%v", err)
	}
}
