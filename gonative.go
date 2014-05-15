package main

import (
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/inconshreveable/go-update"
	"github.com/inconshreveable/go-update/check"
)

// XXX: need checksum verification on these downloads

// XXX: this is hardcoded to 1.2.1 GOARM=6, sorry
const linuxArmUrl = "https://inconshreveable.com/dl/go1.2.1.linux-arm.tar.gz"
const oldDistUrl = "https://go.googlecode.com/files/go%s.%s.tar.gz"
const newDistUrl = "https://storage.googleapis.com/golang/go%s.%s.tar.gz"
const usage = `Usage: gonative [options]

Cross compiled Go binaries are not suitable for production applications
because code in the standard library relies on Cgo for DNS resolution
with the native resolver, access to system certificate roots, and parts of os/user.

gonative is a simple tool which creates a build of Go that can cross compile
to all platforms while still using the Cgo-enabled versions of the stdlib
packages. It does this by downloading the binary distributions for each
platform and copying their libraries into the proper places. It sets
the correct access time so they don't get rebuilt. It also copies
some auto-generated runtime files into the build as well. gonative does
not modify any Go that you have installed and builds Go again in a separate
directory (the current directory by default).
`

var allPlatforms = []Platform{
	Platform{"linux", "386"},
	Platform{"linux", "amd64"},
	Platform{"linux", "arm"},
	Platform{"darwin", "386"},
	Platform{"darwin", "amd64"},
	Platform{"windows", "386"},
	Platform{"windows", "amd64"},
	Platform{"freebsd", "386"},
	Platform{"freebsd", "amd64"},
}

type Platform struct {
	OS   string
	Arch string
}

func (p *Platform) String() string {
	return p.OS + "_" + p.Arch
}

func (p *Platform) DistUrl(version string) string {
	if p.OS == "linux" && p.Arch == "arm" && version == "1.2.1" {
		return linuxArmUrl
	}

	distString := p.OS + "-" + p.Arch
	if p.OS == "darwin" {
		distString += "-osx10.8"
	}

	s := fmt.Sprintf(distUrl(version), version, distString)
	if p.OS == "windows" {
		s = strings.Replace(s, ".tar.gz", ".zip", 1)
	}
	return s
}

func distUrl(version string) string {
	// hosting changed after 1.2.1
	if version > "1.2.1" {
		return newDistUrl
	} else {
		return oldDistUrl
	}
}

type Options struct {
	version    string
	srcPath    string
	targetPath string
	platforms  []Platform
}

func main() {
	opts, err := parseArgs()
	if err != nil {
		fmt.Printf("Error parsing args: %v\n", err)
		os.Exit(1)
	}

	if err = buildGo(opts); err != nil {
		fmt.Printf("Failed to build Go: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Successfuly built Go in %v\n", opts.targetPath)
}

func parseArgs() (*Options, error) {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, usage)
		fmt.Fprintf(os.Stderr, "\nFLAGS\n\n")
		flag.PrintDefaults()
	}
	version := flag.String("version", "1.2.1", "version of Go to build")
	srcPath := flag.String("src", "", "path to go source, empty string mean fetch from internet")
	targetPath := flag.String("target", ".", "target directory to build go in")
	platforms := flag.String("platforms", "", "space separated list of platforms to build, emptry string means all")
	update := flag.Bool("update", false, "ask gonative to update itself")

	flag.Parse()

	if *update {
		result, err := runUpdate()
		if err != nil {
			fmt.Printf("Failed to update: %v\n", err)
		} else {
			fmt.Printf("Updated succesfully to version %v!\n", result.Version)
		}
		os.Exit(0)
	}

	opts := &Options{
		version: *version,
		srcPath: *srcPath,
	}

	var err error
	opts.targetPath, err = filepath.Abs(*targetPath)
	if err != nil {
		return nil, err
	}

	if *platforms == "" {
		opts.platforms = allPlatforms
	} else {
		opts.platforms = make([]Platform, 0)
		for _, pString := range strings.Split(*platforms, " ") {
			parts := strings.Split(pString, "_")
			if len(parts) != 2 {
				return nil, fmt.Errorf("Invalid platform string: %v", pString)
			}
			opts.platforms = append(opts.platforms, Platform{parts[0], parts[1]})
		}
	}

	return opts, nil
}

func buildGo(opts *Options) (err error) {
	src := opts.srcPath
	if src == "" {
		src = "(from internet)"
	}
	fmt.Println("Building go:")
	fmt.Printf("\tVersion: %v\n", opts.version)
	fmt.Printf("\tSource: %v\n", src)
	fmt.Printf("\tTarget: %v\n", opts.targetPath)
	fmt.Printf("\tPlatforms: %v\n", opts.platforms)

	// tells the platform goroutines that the target path is ready
	targetReady := make(chan struct{})

	// platform gorouintes can report an error here
	errors := make(chan error, len(opts.platforms))

	// need to wait for each platform to finish
	var wg sync.WaitGroup
	wg.Add(len(opts.platforms))

	// run all platform fetch/copies in parallel
	for _, p := range opts.platforms {
		go getPlatform(p, opts.targetPath, opts.version, targetReady, errors, &wg)
	}

	// fetch the source from the internet if there's no path to it
	if opts.srcPath == "" {
		srcUrl := fmt.Sprintf(distUrl(opts.version), opts.version, "src")
		fmt.Printf("Fetching Go sources from %s\n", srcUrl)
		opts.srcPath, err = getUrl(srcUrl, "src")
		if err != nil {
			return
		}
		defer os.RemoveAll(opts.srcPath)
		opts.srcPath = filepath.Join(opts.srcPath, "go")
	}

	// copy the source to the target directory
	err = copyRecursive(opts.srcPath, opts.targetPath)
	if err != nil {
		return
	}

	// build Go for the host platform
	err = makeDotBash(filepath.Join(opts.targetPath, "go"))

	// bootstrap compilers for all target platforms
	fmt.Println("Bootstrapping Go compilers")
	for _, p := range opts.platforms {
		err = distBootstrap(filepath.Join(opts.targetPath, "go"), p)
		if err != nil {
			return
		}
	}

	// tell the platform goroutines that the target dir is ready
	close(targetReady)

	// wait for all platforms to finish
	wg.Wait()

	// return error if we failed to get a platform
	select {
	case err := <-errors:
		return err
	default:
		return nil
	}
}

func getDist(p Platform, version string) (string, error) {
	return getUrl(p.DistUrl(version), p.String())
}

func getUrl(url, name string) (path string, err error) {
	fmt.Printf("Downloading: %s\n", url)
	resp, err := http.Get(url)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("Bad response for download: %v", resp.StatusCode)
	}

	fmt.Printf("OK, streaming response: %s\n", url)
	if strings.HasSuffix(url, ".zip") {
		path, err = unpackZip(resp.Body, name)
		if err != nil {
			return
		}
	} else {
		path, err = unpackTgz(resp.Body, name)
		if err != nil {
			return
		}
	}

	fmt.Printf("Download complete: %s\n", url)
	return
}

func unpackZip(rd io.Reader, name string) (path string, err error) {
	f, err := ioutil.TempFile(".", name+"-")
	if err != nil {
		return
	}
	defer os.Remove(f.Name())
	defer f.Close()

	_, err = io.Copy(f, rd)
	if err != nil {
		return
	}

	path, err = ioutil.TempDir(".", name+"-")
	if err != nil {
		return
	}

	return path, exec.Command("unzip", f.Name(), "-d", path).Run()
}

func unpackTgz(rd io.Reader, name string) (path string, err error) {
	path, err = ioutil.TempDir(".", name+"-")
	if err != nil {
		return
	}

	cmd := exec.Command("tar", "xzf", "-", "-C", path)
	wr, err := cmd.StdinPipe()
	if err != nil {
		return
	}

	if err = cmd.Start(); err != nil {
		return
	}

	if _, err = io.Copy(wr, rd); err != nil {
		return
	}
	wr.Close()

	if err = cmd.Wait(); err != nil {
		return
	}
	return
}

// runs make.[bash|bat] in the source directory to build all of the compilers
// and standard library
func makeDotBash(goRoot string) (err error) {
	scriptName := "make.bash"
	if runtime.GOOS == "windows" {
		scriptName = "make.bat"
	}

	scriptPath, err := filepath.Abs(filepath.Join(goRoot, "src", scriptName))
	if err != nil {
		return
	}
	scriptDir := filepath.Dir(scriptPath)

	cmd := exec.Cmd{
		Path:   scriptPath,
		Args:   []string{scriptPath},
		Env:    os.Environ(),
		Dir:    scriptDir,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}

	return cmd.Run()
}

// runs dist bootrap to build the compilers for a target platform
func distBootstrap(goRoot string, p Platform) (err error) {
	// the dist tool gets put in the pkg/tool/{host_platform} directory after we've built
	// the compilers/stdlib for the host platform
	hostPlatform := Platform{runtime.GOOS, runtime.GOARCH}
	scriptPath, err := filepath.Abs(filepath.Join(goRoot, "pkg", "tool", hostPlatform.String(), "dist"))
	if err != nil {
		return
	}

	// but we want to run it from the src directory
	scriptDir, err := filepath.Abs(filepath.Join(goRoot, "src"))
	if err != nil {
		return
	}

	bootstrapCmd := exec.Cmd{
		Path: scriptPath,
		Args: []string{scriptPath, "bootstrap", "-v"},
		Env: append(os.Environ(),
			"GOOS="+p.OS,
			"GOARCH="+p.Arch),
		Dir:    scriptDir,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}

	return bootstrapCmd.Run()
}

func getPlatform(p Platform, targetPath, version string, targetReady chan struct{}, errors chan error, wg *sync.WaitGroup) {
	defer wg.Done()

	// download the binary distribution
	path, err := getDist(p, version)
	if err != nil {
		errors <- err
		return
	}
	defer os.RemoveAll(path)

	// wait for target directory to be ready
	<-targetReady

	// copy over the packages
	targetPkgPath := filepath.Join(targetPath, "go", "pkg")
	srcPkgPath := filepath.Join(path, "go", "pkg", p.String())
	err = copyRecursive(srcPkgPath, targetPkgPath)
	if err != nil {
		errors <- err
		return
	}

	// copy over the auto-generated z_ files
	srcZPath := filepath.Join(path, "go", "src", "pkg", "runtime", "z*_"+p.String())
	targetZPath := filepath.Join(targetPath, "go", "src", "pkg", "runtime")
	cpCmd := fmt.Sprintf("cp -p %s %s", srcZPath, targetZPath)
	err = exec.Command("bash", "-c", cpCmd).Run()

	// change the mod times
	now := time.Now()
	err = filepath.Walk(targetPkgPath, func(path string, info os.FileInfo, err error) error {
		os.Chtimes(path, now, now)
		return nil
	})
	if err != nil {
		errors <- err
		return
	}
}

func copyRecursive(src, dst string) error {
	fmt.Printf("cp -rp %s %s\n", src, dst)
	return exec.Command("cp", "-rp", src, dst).Run()
}

const appVersion = "0.1.7"
const equinoxAppId = "ap_VQ_K1O_27-tPsncKE3E2GszIPm"
const publicKey = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAvMwGMSLLi3bfq6UZesVR
H+/EnPyVqbVTJs3zCiFSnLrXMkOMuXfmf7mC23q1cPaGOIFTfmhcx5/vkda10NJ1
owTAJKXVctC6TUei42vIiBSPsdhzyinNtCdkEkBT2f6Ac58OQV1dUBW/b0fQRQZN
9tEwW7PK1QnR++bmVu2XzoGEw17XZdeDoXftDBgYAzOWDqapZpHETPobL5oQHeQN
CVdCaNbNo52/HL6XKyDGCNudVqiKgIoExPzcOL6KKfvMla1Y4mrrArbuNBlE3qxW
CwmnjtWg+J7vb9rKfZvuVPXPD/RoruZUmHBc1f31KB/QFvn/zXSqeyBcsd6ywCfo
KwIDAQAB
-----END PUBLIC KEY-----`

func runUpdate() (*check.Result, error) {
	params := check.Params{
		AppVersion: appVersion,
		AppId:      equinoxAppId,
	}

	up, err := update.New().VerifySignatureWithPEM([]byte(publicKey))
	if err != nil {
		return nil, err
	}

	result, err, errRecover := params.CheckAndApplyUpdate("https://api.equinox.io/1/Updates", up)
	if err != nil {
		if errRecover != nil {
			return nil, fmt.Errorf("Failed to recover from bad update: %v. Original error: %v", errRecover, err)
		}
		return nil, err
	}

	return result, nil
}
