# Roadmap

## Next
- Check the first positional argument against cobra's subcommand names and aliases before running, and name the closest one
- Read arguments from docker-compose `command`, systemd `ExecStart` and Dockerfile `CMD` lines directly, not only after `--`
- `--image-ref`: check out the commit an image was built from, using the `org.opencontainers.image.revision` label, before building

## Later
- urfave/cli and kingpin, with the same hook-and-confirm approach
- A GitHub Action that renders a chart and comments the findings on a pull request
- Environment variables that the program reads through flag libraries such as `ff` or cobra's viper binding
