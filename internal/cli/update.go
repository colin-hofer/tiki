package cli

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"tiki/internal/tiki"
)

// Only scripts/build-cli.sh enables this. Replacing a server or development
// executable with a downloaded CLI would discard its bundled assets.
var standaloneCLI = "false"

func (a *app) updateCommand() *cobra.Command {
	c := &cobra.Command{
		Use: "update", Short: "Update the installed CLI from your configured server", Args: cobra.NoArgs,
		Long: "Download and install the CLI bundled with your configured server.\nNo sign-in is required. Downloads time out after 5 minutes; use --timeout to override.\nOnly standalone CLI installations can self-update; server and development builds cannot.",
	}
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		if !cmd.Flags().Changed("timeout") {
			a.timeout = 5 * time.Minute
		}
		path, err := os.Executable()
		if err != nil {
			return err
		}
		return a.updateCLI(cmd.Context(), path)
	}
	return c
}

func (a *app) updateCLI(ctx context.Context, path string) error {
	if standaloneCLI != "true" {
		return fmt.Errorf("this server or development build cannot self-update; rebuild/redeploy it, or install the standalone CLI from your workspace")
	}
	if (runtime.GOOS != "linux" && runtime.GOOS != "darwin") || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return fmt.Errorf("CLI updates support Linux and macOS on x86-64 and ARM64")
	}
	if a.timeout <= 0 {
		return &tiki.Error{Code: "validation", Message: "timeout must be positive"}
	}
	server, err := a.serverURL()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	current, err := os.Open(path)
	if err != nil {
		return err
	}
	defer current.Close()
	info, err := current.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("CLI executable is not a regular file: %s", path)
	}
	// Stage beside the executable for an atomic rename, including custom paths
	// and symlink targets. Check directory permissions before downloading.
	directory, err := os.MkdirTemp(filepath.Dir(path), ".tiki-update-*")
	if err != nil {
		return fmt.Errorf("cannot update %s; its directory must be writable: %w", path, err)
	}
	defer os.RemoveAll(directory)
	url := server + "/api/v1/cli/downloads/" + runtime.GOOS + "-" + runtime.GOARCH
	checksum, err := downloadUpdate(ctx, url+".sha256", 1024)
	if err != nil {
		return err
	}
	expected, err := hex.DecodeString(strings.TrimSpace(string(checksum)))
	if err != nil || len(expected) != sha256.Size {
		return fmt.Errorf("server returned an invalid CLI checksum; nothing was installed")
	}
	archive, err := downloadUpdate(ctx, url+".gz", 64<<20)
	if err != nil {
		return err
	}
	actual := sha256.Sum256(archive)
	if !bytes.Equal(actual[:], expected) {
		return fmt.Errorf("CLI checksum mismatch; nothing was installed, try again")
	}
	z, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("invalid CLI archive: %w", err)
	}
	defer z.Close()
	replacement, err := os.Create(filepath.Join(directory, "tiki"))
	if err != nil {
		return err
	}
	defer replacement.Close()
	newHash := sha256.New()
	// Bound decompression as well as the compressed download.
	n, err := io.Copy(io.MultiWriter(replacement, newHash), io.LimitReader(z, (128<<20)+1))
	if err != nil {
		return fmt.Errorf("cannot unpack CLI: %w", err)
	}
	if n == 0 || n > 128<<20 {
		return fmt.Errorf("unpacked CLI must contain between 1 byte and 128 MiB")
	}
	oldHash := sha256.New()
	if _, err := io.Copy(oldHash, current); err != nil {
		return err
	}
	updated := !bytes.Equal(oldHash.Sum(nil), newHash.Sum(nil))
	if updated {
		if err := replacement.Chmod(info.Mode().Perm()); err != nil {
			return err
		}
		if err := replacement.Sync(); err != nil {
			return err
		}
		if err := replacement.Close(); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		latest, err := os.Stat(path)
		if err != nil {
			return err
		}
		if !os.SameFile(info, latest) {
			return fmt.Errorf("CLI changed during the update; nothing was installed, try again")
		}
		if err := os.Rename(replacement.Name(), path); err != nil {
			return fmt.Errorf("cannot replace CLI; nothing was installed: %w", err)
		}
	}
	if a.json {
		return a.print(map[string]any{"updated": updated, "path": path, "server": server})
	}
	if !updated {
		_, err = fmt.Fprintln(a.out, "Tiki is already up to date with", terminalText(server))
	} else {
		_, err = fmt.Fprintf(a.out, "Updated Tiki at %s from %s\n", terminalText(path), terminalText(server))
	}
	return err
}

// Downloads are public. Never send a session token or follow a redirect to a
// different artifact host, matching the shell installer's trust boundary.
func downloadUpdate(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := httpClient.Do(req)
	if err != nil {
		return nil, &tiki.Error{Code: "transport", Message: "cannot download CLI update: " + err.Error()}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, &tiki.Error{Code: "transport", Message: "cannot download CLI update: server returned " + response.Status}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, &tiki.Error{Code: "transport", Message: "cannot download CLI update: " + err.Error()}
	}
	if int64(len(data)) > limit {
		return nil, &tiki.Error{Code: "transport", Message: "CLI update download exceeds size limit"}
	}
	return data, nil
}
