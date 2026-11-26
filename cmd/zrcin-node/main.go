// Command zrcin-node is the node-side launcher (docs/14 §7): it installs the
// scan node on a host, keeps the tunnel to the platform open, supervises the
// scan process and executes the platform's directives. All real work lives
// here in Go; the bootstrap one-liner only downloads this binary.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd {
	case "install":
		fs := flag.NewFlagSet("install", flag.ContinueOnError)
		join := fs.String("join", "", "接入地址：<平台公网地址>/join/<接入令牌>")
		root := fs.String("root", "", "安装根目录（默认 /opt/zrcin-node，非 root 安装为 ~/.zrcin-node）")
		if perr := fs.Parse(args); perr != nil {
			err = perr
			break
		}
		if strings.TrimSpace(*join) == "" {
			err = errors.New("install 需要 --join <平台地址>/join/<接入令牌>")
			break
		}
		err = runInstall(ctx, *join, *root)
	case "run":
		fs := flag.NewFlagSet("run", flag.ContinueOnError)
		root := fs.String("root", "", "安装根目录（默认与 install 相同）")
		if perr := fs.Parse(args); perr != nil {
			err = perr
			break
		}
		err = runNode(ctx, *root)
	case "uninstall":
		fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
		root := fs.String("root", "", "安装根目录")
		if perr := fs.Parse(args); perr != nil {
			err = perr
			break
		}
		err = runUninstall(ctx, *root)
	case "status":
		fs := flag.NewFlagSet("status", flag.ContinueOnError)
		root := fs.String("root", "", "安装根目录")
		if perr := fs.Parse(args); perr != nil {
			err = perr
			break
		}
		err = runStatus(*root)
	case "-h", "--help", "help":
		usage()
		return
	default:
		err = fmt.Errorf("未知子命令 %q", cmd)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "zrcin-node: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `zrcin-node —— 远端 scan 节点启动器

用法：
  zrcin-node install --join <平台地址>/join/<接入令牌> [--root <目录>]
  zrcin-node run      [--root <目录>]
  zrcin-node uninstall[--root <目录>]
  zrcin-node status   [--root <目录>]

安装根默认 /opt/zrcin-node（root）；非 root 安装为 ~/.zrcin-node。
`)
}

// resolveRoot applies the --root flag or the install-time default.
func resolveRoot(flagValue string) (string, error) {
	if strings.TrimSpace(flagValue) != "" {
		return strings.TrimSpace(flagValue), nil
	}
	return defaultRootFor()
}
