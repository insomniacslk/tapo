# tapo CLI

This command-line tool lets you interact with Tapo devices directly.

You need to create a Tapo account on the Tapo app (make sure that the password
is not longer than 8 characters, it seems the local API doesn't work otherwise).

Once you have created your credentials, you need a configurationfile. See the "Configuration file" section below.

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

## Configuration file

The configuration file has the following format:

```
{
	"email": "your-tapo-email",
	"password": "your-tapo-password",
	"debug": false
}

```

This file has different locations depending on your operating system:

* if you are running Linux, the default location is `~/.config/tapo/config.json`
* if you are running MacOS, the default location is `~/Library/Application Support/tapo/config.json`.


In any case, you can check the default location for the OS you are on, by running `./tapo -h` and checking the default value in the output for the --config flag. See the build instructions below for building the `tapo` CLI.

## Running the CLI

Once the `tapo` CLI is built, you can run it. Depending on your operating system you might have to run it with `./tapo` or just `tapo`. You can also move it somewhere in your `PATH` so to run it just with `tapo`. Either way, the output will look similar to the below:

```
$ tapo -h
Usage: tapo <flags> [command]

command is one of on, off, info, energy, cloud-list, list, discover (local broadcast)

  -a, --addr ip           IP address of the Tapo device
  -c, --config string     Configuration file (default "/home/insomniac/.config/tapo/config.json")
  -d, --debug             Enable debug logs
  -e, --email string      E-mail for login
  -f, --format list       Template for printing each line of a discovered device, works with list, `discover` and `cloud-list`, fields may differ across commands. It uses Go's text/template syntax (default "{{.Idx}}) name={{.Name}} ip={{.IP}} mac={{.MAC}} type={{.Type}} model={{.Model}} deviceid={{.ID}}\n")
  -n, --name string       Name of the Tapo device. This is slow, it will perform a local discovery first. Ignored if --addr is specified
  -p, --password string   Password for login
pflag: help requested
```

The subcommands are:

* `on`: turn you Tapo device on
* `off`: turn your Tapo device off
* `info`: print verbose information about your Tapo device
* `energy`: print energy information about your Tapo device, if supported (for example on Tapo P110, but not on P100)
* `cloud-list`: shows a list of registered Tapo devices, similar to the one you get on the Tapo app
* `list`: looks for Tapo devices connected to your local network. The output might be different than `cloud-list`
* `discover`: similar to `list`, but it runs a lightweight discovery without printing the extra information shown by `list`


For some commands you have to specify either the plug's IP address (with `-a/--address`) or the device name (with `-n/--name`). The latter is slower because it requires a local scan that will fetch device info and look for a device with that name (case sensitive).
