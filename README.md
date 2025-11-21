# tapo

Go library for managing Tapo P100 and P110 plugs.

See [cmd/tapo](cmd/tapo) for a sample CLI.

See [cmd/tapoweb](cmd/tapoweb) for a sample web interface.

## Troubleshooting

Login failures can manifest as `Communication error (1003)`, and this can happen
if your Tapo plugs have a firmware version >= 1.4.0 and you didn't enable third-party
compatibility. See [this issue](https://github.com/insomniacslk/tapo/issues/4) for
more details.

The fix is to open your Tapo app, tap on "Me", then "Third-party services", and then
enable "Third-party compatibility".
