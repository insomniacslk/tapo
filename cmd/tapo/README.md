# tapo CLI

This command-line tool lets you interact with Tapo devices directly.

You need to create a Tapo account on the Tapo app (make sure that the password
is not longer than 8 characters, it seems the local API doesn't work otherwise).

Once you have created your credentials, you need a configurationfile.
* if you are running Linux, the default location is `~/.config/tapo/config.json`
* if you are running MacOS, the default location is `~/Library/Application Support/tapo/config.json`.

In any case, you can check the default location for the OS you are on, by running `./tapo -h` and checking the default value in the output for the --config flag. See the build instructions below for building the `tapo` CLI.

## Build instructions

This is a Go program, so you need a working `go` installation. A relatively recent version is required.

Then just run `go build` in the current directory (where this README file is located), after cloning this repository.

In short:
```
git clone https://github.com/insomniacslk/tapo
cd tapo/cmd/tapo
go build
```

If all goes well, you should see a `tapo` executable in the current directory.
