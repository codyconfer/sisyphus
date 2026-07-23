//go:build windows

package duckdb

func secureUmask() func() { return func() {} }
