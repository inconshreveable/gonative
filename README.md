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
not modify any Go that you have installed and builds a new installaion of 
Go in a separate directory (the current directory by default).

Once you have a toolchain for cross-compilation, you can use tools like
[gox](https://github.com/mitchellh/gox) to cross-compile native builds easily.

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
they won't get rebuilt. It also copies some necessary auto-generated runtime source
files for each platform (z\*\_) into the source directory to make it all work.

### Example with gox:

Here's an example of how to cross-compile a project:

    $ go get github.com/mitchellh/gox
    $ go get github.com/inconshreveable/gonative
    $ cd /your/project
    $ gonative
    $ PATH=$PWD/go/bin/:$PATH gox
    
This isn't the most optimal way of doing things though. You only ever need one gonative-built 
Go toolchain. And with the proper GOPATH set up, you don't need to be
in your project's working directory. I use it mostly like this:

#### One time only setup:

    $ go get github.com/mitchellh/gox
    $ go get github.com/inconshreveable/gonative
    $ mkdir -p /usr/local/gonative
    $ cd /usr/local/gonative
    $ gonative
    
#### Building a project:

    $ PATH=/usr/local/gonative/go/bin/:$PATH gox github.com/your-name/application-name
    
### Open Issues

- no checksum validation of downloaded packages
- gonative won't run on Windows because it uses unzip/tar unix utilities

### Caveats
- linux/arm won't work for any other version than 1.2.1 since I'm hosting that build myself
- gonative uses a GOARM=6 linux/arm build, it won't work for ARMv5 targets (default for any cross-compiled ARM builds anyways)
- linux_386 binaries that use native libs depend on 32-bit libc/libpthread/elf loader. some 64-bit linux distributions might not have those installed by default
