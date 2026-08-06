//go:build linux

// Command plumbline-jail sets up a pivot_root sandbox then execs the target.
// Invoked only via `unshare` from the crawler; not a user-facing command.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: plumbline-jail <jail-root> <target> [args...]\n")
		os.Exit(2)
	}
	root := os.Args[1]
	target := os.Args[2]
	args := os.Args[2:]

	if err := setupJail(root); err != nil {
		fmt.Fprintf(os.Stderr, "jail setup: %v\n", err)
		os.Exit(3)
	}
	if err := syscall.Exec(target, args, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "exec: %v\n", err)
		os.Exit(4)
	}
}

func setupJail(root string) error {
	if err := syscall.Mount(root, root, "", syscall.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind root: %w", err)
	}
	for _, d := range []string{"bin", "lib", "lib64", "usr", "proc", "tmp", "oldroot", "dev", "etc"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			return err
		}
	}
	if err := bindRO("/bin", filepath.Join(root, "bin")); err != nil {
		return err
	}
	if _, err := os.Stat("/lib64"); err == nil {
		if err := rbind("/lib64", filepath.Join(root, "lib64")); err != nil {
			return err
		}
	}
	if err := rbind("/lib", filepath.Join(root, "lib")); err != nil {
		return err
	}
	if err := rbind("/usr", filepath.Join(root, "usr")); err != nil {
		return err
	}
	if err := syscall.Mount("tmpfs", filepath.Join(root, "tmp"), "tmpfs", 0, "size=64m,mode=1777"); err != nil {
		return fmt.Errorf("tmpfs: %w", err)
	}
	if err := syscall.Mount("proc", filepath.Join(root, "proc"), "proc", 0, ""); err != nil {
		return fmt.Errorf("proc: %w", err)
	}
	if err := syscall.Mount("tmpfs", filepath.Join(root, "dev"), "tmpfs", 0, ""); err != nil {
		return fmt.Errorf("dev tmpfs: %w", err)
	}
	for _, n := range []string{"null", "zero", "urandom", "random"} {
		src := filepath.Join("/dev", n)
		dst := filepath.Join(root, "dev", n)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		f, err := os.OpenFile(dst, os.O_CREATE, 0o666)
		if err != nil {
			return fmt.Errorf("dev node %s: %w", n, err)
		}
		_ = f.Close()
		if err := syscall.Mount(src, dst, "", syscall.MS_BIND, ""); err != nil {
			return fmt.Errorf("bind %s: %w", src, err)
		}
	}

	oldwd, err := os.Getwd()
	if err != nil {
		return err
	}
	_ = oldwd
	if err := os.Chdir(root); err != nil {
		return err
	}
	if err := syscall.PivotRoot(".", "oldroot"); err != nil {
		return fmt.Errorf("pivot_root: %w", err)
	}
	if err := os.Chdir("/"); err != nil {
		return err
	}
	if err := syscall.Unmount("/oldroot", syscall.MNT_DETACH); err != nil {
		return fmt.Errorf("umount oldroot: %w", err)
	}
	_ = os.Remove("/oldroot")
	return nil
}

func bindRO(src, dst string) error {
	if err := syscall.Mount(src, dst, "", syscall.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind %s: %w", src, err)
	}
	if err := syscall.Mount("", dst, "", syscall.MS_REMOUNT|syscall.MS_BIND|syscall.MS_RDONLY, ""); err != nil {
		return fmt.Errorf("remount ro %s: %w", dst, err)
	}
	return nil
}

func rbind(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	if err := syscall.Mount(src, dst, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
		return fmt.Errorf("rbind %s: %w", src, err)
	}
	return nil
}
