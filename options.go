package ddmcproxy

type Options struct {
	Config string `short:"c" required:"" env:"DDMCPROXY_CONFIG" help:"Config file path."`
}
