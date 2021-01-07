Elementum daemon [![Build Status](https://travis-ci.org/elgatito/elementum.svg?branch=master)](https://travis-ci.org/elgatito/elementum)
======

Fork of the great [Pulsar daemon](https://github.com/steeve/pulsar) and [Quasar daemon](https://github.com/scakemyer/quasar)

1. Build the [cross-compiler](https://github.com/ElementumOrg/cross-compiler) images,
    or alternatively, pull the cross-compiler images from [Docker Hub](https://hub.docker.com/r/elementumorg/cross-compiler):

    ```
    make pull-all
    ```

    Or for a specific platform:
    ```
    make pull PLATFORM=android-x64
    ```

2. Set GOPATH

    ```
    export GOPATH="~/go"
    ```

3. go get

    ```
    go get -d github.com/elgatito/elementum
    ```

    For Windows support, but required for all builds, you also need:

    ```
    go get github.com/mattn/go-isatty
    ```

4. Build libtorrent-go libraries:

    ```
    make libs
    ```

5. Make specific platforms, or all of them:

    Linux-x64
    ```
    make linux-x64
    ```

    Darwin-x64
    ```
    make darwin-x64
    ```

    Windows
    ```
    make windows-x86
    ```

    All platforms
    ```
    make
    ```

Find memory leaks

To find memory leaks, we can use Valgrind or use Sanitizers.

```
/bin/bash test_build.sh sanitize
```
This will build the binary with enabled sanitizer, so just run it and wait for errors in the console.

```
valgrind --leak-check=full ./elementum -disableBackup
```
This will run the binary with Valgrind. When you cose the process, Valgrind will show statistics.
It is better to use usual binary, without sanitizer, and add backup disable options, as well as disable Kodi library integration. Or it will take a lot of time/CPU at the startup (unless you do need it).