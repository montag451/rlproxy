package main

import (
	"fmt"
	"io"
	"log/syslog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/rs/zerolog"
	"gopkg.in/yaml.v3"
)

type StringSlice []string

func (ss *StringSlice) String() string {
	if ss == nil {
		return fmt.Sprint(StringSlice{})
	}
	return fmt.Sprint(*ss)
}

func (ss *StringSlice) Set(s string) error {
	*ss = strings.Split(s, ",")
	return nil
}

type HumanBytes uint64

func (h *HumanBytes) String() string {
	if h == nil {
		return strconv.FormatUint(uint64(HumanBytes(0)), 10)
	}
	return strconv.FormatUint(uint64(*h), 10)
}

func (h *HumanBytes) Set(s string) error {
	n, err := humanize.ParseBytes(s)
	if err != nil {
		return err
	}
	*h = HumanBytes(n)
	return nil
}

func (h *HumanBytes) UnmarshalYAML(node *yaml.Node) error {
	var v interface{}
	if err := node.Decode(&v); err != nil {
		return err
	}
	switch v := v.(type) {
	case string:
		return h.Set(v)
	case float64:
		*h = HumanBytes(v)
		return nil
	default:
		return fmt.Errorf("cannot unmarshal %q into a bytes number", node.ShortTag())
	}
}

type configuration struct {
	logger    zerolog.Logger
	Name      string        `yaml:"name" flag:"name,,instance name"`
	Addrs     StringSlice   `yaml:"addrs" flag:"addrs,127.0.0.1:12000,bind addresses"`
	Upstream  string        `yaml:"upstream" flag:"upstream,,upstream address"`
	Rate      HumanBytes    `yaml:"rate" flag:"rate,,incoming traffic rate limit"`
	Burst     HumanBytes    `yaml:"burst" flag:"burst,,allowed traffic burst"`
	PerClient bool          `yaml:"per_client" flag:"per-client,,apply rate limit per client"`
	NoSplice  bool          `yaml:"no_splice" flag:"no-splice,,disable the use of the splice syscall (Linux only)"`
	BufSize   HumanBytes    `yaml:"buf_size" flag:"buf-size,,buffer size to use to transfer data between the downstream clients and the upstream server"`
	Logging   loggingConfig `yaml:"logging"`
}

type loggingConfig struct {
	Level   string        `yaml:"level" flag:"log-level,info,log level"`
	Syslog  syslogConfig  `yaml:"syslog"`
	Console consoleConfig `yaml:"console"`
}

type consoleConfig struct {
	Enabled   bool `yaml:"enabled" flag:"log-console,true,send log on console"`
	Pretty    bool `yaml:"pretty" flag:"log-console-pretty,,enable pretty log on console"`
	UseStderr bool `yaml:"use_stderr" flag:"log-console-stderr,,send logs on stderr"`
}

type syslogConfig struct {
	Enabled  bool   `yaml:"enabled" flag:"log-syslog,,send log with syslog"`
	Facility string `yaml:"facility" flag:"log-syslog-facility,local0,syslog facility to use"`
}

func parseConfig(c *configuration, cf string) error {
	f, err := os.Open(cf)
	if err != nil {
		return fmt.Errorf("failed to open conf file %q: %v", cf, err)
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil {
		return fmt.Errorf("failed to parse conf file %q: %v", cf, err)
	}
	return nil
}

var syslogFacilities = map[string]syslog.Priority{
	"kern":     syslog.LOG_KERN,
	"user":     syslog.LOG_USER,
	"mail":     syslog.LOG_MAIL,
	"daemon":   syslog.LOG_DAEMON,
	"auth":     syslog.LOG_AUTH,
	"syslog":   syslog.LOG_SYSLOG,
	"lpr":      syslog.LOG_LPR,
	"news":     syslog.LOG_NEWS,
	"uucp":     syslog.LOG_UUCP,
	"cron":     syslog.LOG_CRON,
	"authpriv": syslog.LOG_AUTHPRIV,
	"ftp":      syslog.LOG_FTP,
	"local0":   syslog.LOG_LOCAL0,
	"local1":   syslog.LOG_LOCAL1,
	"local2":   syslog.LOG_LOCAL2,
	"local3":   syslog.LOG_LOCAL3,
	"local4":   syslog.LOG_LOCAL4,
	"local5":   syslog.LOG_LOCAL5,
	"local6":   syslog.LOG_LOCAL6,
	"local7":   syslog.LOG_LOCAL7,
}

func loggerFromConfig(conf *loggingConfig) (*zerolog.Logger, error) {
	const app = "rlproxy"
	level, err := zerolog.ParseLevel(conf.Level)
	if err != nil {
		return nil, fmt.Errorf("invalid log level %q: %v", conf.Level, err)
	}
	writers := []io.Writer{}
	if conf.Console.Enabled {
		conf := conf.Console
		out := os.Stdout
		if conf.UseStderr {
			out = os.Stderr
		}
		if conf.Pretty {
			w := zerolog.NewConsoleWriter()
			w.Out = out
			w.TimeFormat = time.RFC3339
			writers = append(writers, w)
		} else {
			writers = append(writers, out)
		}
	}
	if conf.Syslog.Enabled {
		conf := conf.Syslog
		facility, ok := syslogFacilities[conf.Facility]
		if !ok {
			return nil, fmt.Errorf("unknown syslog facility %q", conf.Facility)
		}
		prio := syslog.LOG_INFO | facility
		w, err := syslog.New(prio, app)
		if err != nil {
			return nil, fmt.Errorf("failed to create syslog logger: %v", err)
		}
		writers = append(writers, zerolog.SyslogCEEWriter(w))
	}
	mw := zerolog.MultiLevelWriter(writers...)
	logger := zerolog.New(mw).Level(level).With().
		Timestamp().
		Str("app", app).
		Logger()
	return &logger, nil
}
