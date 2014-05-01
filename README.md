# gonative

Cross compiled Go binaries are not suitable for production applications
because code in the standard library relies on Cgo for DNS resolution
with the native resolver, access to system certificate roots, and parts of os/user.

gonative is a simple tool which creates a build of Go that can cross compile
to all platforms while still using the Cgo-enabled versions of the stdlib
packages. It does this by downloading the binary distributions for each
platform and copying their libraries into the proper places. It sets
the correct mod time so they don't get rebuilt. It also copies
some auto-generated runtime files into the build as well. gonative does
not modify any Go that you have installed and builds Go again in a separate
directory (the current directory by default).

Once you have a toolchain for cross-compilation, you can use tools like
github.com/mitchellh/gox to build them.

gonative will not help you if your own packages rely on cgo

### Installation

go get github.com/inconshreveable/gonative

### Running
By default, gonative will build a toolchain in a directory called 'go' in your working directory.

    gonative

To build a particular version of Go (default is 1.2.1):

    gonative -version=1.1

For options and help:

    gonative -help

### How it works

gonative downloads the go source code and compiles it for your host platform.
It then bootstraps the toolchain for all target platforms (but does not compile the standard library).
Then, it fetches the official binary distributions for all target platforms and copies
each pkg/OS\_ARCH directory into the toolchain so that you will link with natively-compiled versions
of the standard library. It walks all of the copied standard library and sets their modtimes so that
they won't get rebuilt. It also copies some necessary auto-generated runtime files for each platform
(z\*\_) into the source directory to make it all work.

### Open Issues

- linux/arm build pulls a 1.2.1 GOARM=6 build, won't work for ARMv5 platforms
- no checksum validation of downloaded packages
- gonative won't run on Windows because it uses unzip/tar unix utilities
