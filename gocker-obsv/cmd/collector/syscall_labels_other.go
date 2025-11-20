//go:build !(linux && amd64)

package main

import "fmt"

func labelSys(sysnr uint32) string {
	return fmt.Sprintf("%d", sysnr)
}
