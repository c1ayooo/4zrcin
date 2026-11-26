package main

import (
	"fmt"
	"net"
	"strconv"
)

// The tunnel mirrors do not have to use the platform's own port numbers. A
// node host runs other things — an operator's own tunnels, another instance's
// services — and needing 8080 and 18777 free there made installs fail for a
// reason the node never cared about. The launcher allocates two free loopback
// ports at install and keeps them in its config; the platform learns them
// from the heartbeat and points the session's API base at the console mirror
// (docs/14 §7.2).
const (
	mirrorLoopback = "127.0.0.1"
)

// ensureMirrors makes sure the config names two loopback mirror ports that
// are free right now. It allocates missing ones and replaces any that another
// process took since the last run, and reports whether the config changed.
func ensureMirrors(cfg *Config) (bool, error) {
	changed := false
	if !mirrorFree(cfg.MirrorHub) {
		addr, err := allocateLoopback()
		if err != nil {
			return false, fmt.Errorf("分配 Hub 镜像端口：%w", err)
		}
		cfg.MirrorHub = addr
		changed = true
	}
	if !mirrorFree(cfg.MirrorConsole) {
		addr, err := allocateLoopback()
		if err != nil {
			return false, fmt.Errorf("分配控制台镜像端口：%w", err)
		}
		cfg.MirrorConsole = addr
		changed = true
	}
	return changed, nil
}

// mirrorFree reports whether addr is a valid loopback host:port nothing is
// listening on.
func mirrorFree(addr string) bool {
	_, port, ok := hostPortSplit(addr)
	if !ok || port == "0" {
		return false
	}
	ln, err := net.Listen("tcp", mirrorLoopback+":"+port)
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// allocateLoopback asks the OS for a free loopback port. Closing the listener
// before the tunnel binds it leaves a small window another process could take;
// the next run notices and picks again.
func allocateLoopback() (string, error) {
	ln, err := net.Listen("tcp", mirrorLoopback+":0")
	if err != nil {
		return "", err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return net.JoinHostPort(mirrorLoopback, strconv.Itoa(port)), nil
}

// mirrorPort returns the port of a mirror address, or "" when it is unset.
func mirrorPort(addr string) string {
	_, port, ok := hostPortSplit(addr)
	if !ok {
		return ""
	}
	return port
}

// portNumber is the numeric port of a mirror address, 0 when unset.
func portNumber(addr string) int {
	port := mirrorPort(addr)
	n, err := strconv.Atoi(port)
	if err != nil {
		return 0
	}
	return n
}
