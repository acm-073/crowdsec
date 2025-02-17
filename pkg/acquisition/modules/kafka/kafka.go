package kafkaacquisition

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"
	log "github.com/sirupsen/logrus"
	"gopkg.in/tomb.v2"
	"gopkg.in/yaml.v2"

	"github.com/crowdsecurity/go-cs-lib/trace"

	"github.com/crowdsecurity/crowdsec/pkg/acquisition/configuration"
	"github.com/crowdsecurity/crowdsec/pkg/types"
)

const dataSourceName = "kafka"

var linesRead = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "cs_kafkasource_hits_total",
		Help: "Total lines that were read from topic",
	},
	[]string{"topic"})

type KafkaConfiguration struct {
	Brokers                           []string    `yaml:"brokers"`
	Topic                             string      `yaml:"topic"`
	GroupID                           string      `yaml:"group_id"`
	Partition                         int         `yaml:"partition"`
	Timeout                           string      `yaml:"timeout"`
	TLS                               *TLSConfig  `yaml:"tls"`
	SASL                              *SASLConfig `yaml:"sasl"`
	configuration.DataSourceCommonCfg `yaml:",inline"`
}

type TLSConfig struct {
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
	ClientCert         string `yaml:"client_cert"`
	ClientKey          string `yaml:"client_key"`
	CaCert             string `yaml:"ca_cert"`
}

type SASLConfig struct {
	Mechanism string `yaml:"mechanism"`
	Username  string `yaml:"username"`
	Password  string `yaml:"password"`
	UseSSL    bool   `yaml:"use_ssl"`
}

type KafkaSource struct {
	metricsLevel int
	Config       KafkaConfiguration
	logger       *log.Entry
	Reader       *kafka.Reader
}

func (k *KafkaSource) GetUuid() string {
	return k.Config.UniqueId
}

func (k *KafkaSource) UnmarshalConfig(yamlConfig []byte) error {
	k.Config = KafkaConfiguration{}

	err := yaml.UnmarshalStrict(yamlConfig, &k.Config)
	if err != nil {
		return fmt.Errorf("cannot parse %s datasource configuration: %w", dataSourceName, err)
	}

	if len(k.Config.Brokers) == 0 {
		return fmt.Errorf("cannot create a %s reader with an empty list of broker addresses", dataSourceName)
	}

	if k.Config.Topic == "" {
		return fmt.Errorf("cannot create a %s reader with am empty topic", dataSourceName)
	}

	if k.Config.Mode == "" {
		k.Config.Mode = configuration.TAIL_MODE
	}

	k.logger.Debugf("successfully parsed kafka configuration : %+v", k.Config)

	return err
}

func (k *KafkaSource) Configure(yamlConfig []byte, logger *log.Entry, metricsLevel int) error {
	k.logger = logger
	k.metricsLevel = metricsLevel

	k.logger.Debugf("start configuring %s source", dataSourceName)

	err := k.UnmarshalConfig(yamlConfig)
	if err != nil {
		return err
	}

	dialer, err := k.Config.NewDialer()
	if err != nil {
		return fmt.Errorf("cannot create %s dialer: %w", dataSourceName, err)
	}

	k.Reader, err = k.Config.NewReader(dialer, k.logger)
	if err != nil {
		return fmt.Errorf("cannote create %s reader: %w", dataSourceName, err)
	}

	if k.Reader == nil {
		return fmt.Errorf("cannot create %s reader", dataSourceName)
	}

	k.logger.Debugf("successfully configured %s source", dataSourceName)

	return nil
}

func (k *KafkaSource) ConfigureByDSN(string, map[string]string, *log.Entry, string) error {
	return fmt.Errorf("%s datasource does not support command-line acquisition", dataSourceName)
}

func (k *KafkaSource) GetMode() string {
	return k.Config.Mode
}

func (k *KafkaSource) GetName() string {
	return dataSourceName
}

func (k *KafkaSource) OneShotAcquisition(_ context.Context, _ chan types.Event, _ *tomb.Tomb) error {
	return fmt.Errorf("%s datasource does not support one-shot acquisition", dataSourceName)
}

func (k *KafkaSource) CanRun() error {
	return nil
}

func (k *KafkaSource) GetMetrics() []prometheus.Collector {
	return []prometheus.Collector{linesRead}
}

func (k *KafkaSource) GetAggregMetrics() []prometheus.Collector {
	return []prometheus.Collector{linesRead}
}

func (k *KafkaSource) Dump() interface{} {
	return k
}

func (k *KafkaSource) ReadMessage(ctx context.Context, out chan types.Event) error {
	// Start processing from latest Offset
	k.Reader.SetOffsetAt(ctx, time.Now())

	for {
		k.logger.Tracef("reading message from topic '%s'", k.Config.Topic)

		m, err := k.Reader.ReadMessage(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}

			k.logger.Errorln(fmt.Errorf("while reading %s message: %w", dataSourceName, err))

			continue
		}

		k.logger.Tracef("got message: %s", string(m.Value))
		l := types.Line{
			Raw:     string(m.Value),
			Labels:  k.Config.Labels,
			Time:    m.Time.UTC(),
			Src:     k.Config.Topic,
			Process: true,
			Module:  k.GetName(),
		}
		k.logger.Tracef("line with message read from topic '%s': %+v", k.Config.Topic, l)

		if k.metricsLevel != configuration.METRICS_NONE {
			linesRead.With(prometheus.Labels{"topic": k.Config.Topic}).Inc()
		}

		evt := types.MakeEvent(k.Config.UseTimeMachine, types.LOG, true)
		evt.Line = l
		out <- evt
	}
}

func (k *KafkaSource) RunReader(ctx context.Context, out chan types.Event, t *tomb.Tomb) error {
	k.logger.Debugf("starting %s datasource reader goroutine with configuration %+v", dataSourceName, k.Config)
	t.Go(func() error {
		return k.ReadMessage(ctx, out)
	})
	//nolint //fp
	for {
		select {
		case <-t.Dying():
			k.logger.Infof("%s datasource topic %s stopping", dataSourceName, k.Config.Topic)
			if err := k.Reader.Close(); err != nil {
				return fmt.Errorf("while closing  %s reader on topic '%s': %w", dataSourceName, k.Config.Topic, err)
			}
			return nil
		}
	}
}

func (k *KafkaSource) StreamingAcquisition(ctx context.Context, out chan types.Event, t *tomb.Tomb) error {
	k.logger.Infof("start reader on brokers '%+v' with topic '%s'", k.Config.Brokers, k.Config.Topic)

	t.Go(func() error {
		defer trace.CatchPanic("crowdsec/acquis/kafka/live")
		return k.RunReader(ctx, out, t)
	})

	return nil
}

func (kc *KafkaConfiguration) NewTLSConfig() (*tls.Config, error) {
	tlsConfig := tls.Config{
		InsecureSkipVerify: kc.TLS.InsecureSkipVerify,
	}

	cert, err := tls.LoadX509KeyPair(kc.TLS.ClientCert, kc.TLS.ClientKey)
	if err != nil {
		return &tlsConfig, err
	}

	tlsConfig.Certificates = []tls.Certificate{cert}

	caCert, err := os.ReadFile(kc.TLS.CaCert)
	if err != nil {
		return &tlsConfig, err
	}

	caCertPool, err := x509.SystemCertPool()
	if err != nil {
		return &tlsConfig, fmt.Errorf("unable to load system CA certificates: %w", err)
	}

	if caCertPool == nil {
		caCertPool = x509.NewCertPool()
	}

	caCertPool.AppendCertsFromPEM(caCert)
	tlsConfig.RootCAs = caCertPool

	return &tlsConfig, err
}

func (kc *KafkaConfiguration) NewSASLConfig() (sasl.Mechanism, error) {
	if kc.SASL.Mechanism == "PLAIN" {
		mechanism := plain.Mechanism{
			Username: kc.SASL.Username,
			Password: kc.SASL.Password,
		}
		return mechanism, nil
	}
	return nil, fmt.Errorf("unsupported sasl mechanism: %s", kc.SASL.Mechanism)
}

func (kc *KafkaConfiguration) NewDialer() (*kafka.Dialer, error) {
	dialer := &kafka.Dialer{}
	var timeoutDuration time.Duration
	timeoutDuration = time.Duration(10) * time.Second
	if kc.Timeout != "" {
		intTimeout, err := strconv.Atoi(kc.Timeout)
		if err != nil {
			return dialer, err
		}
		timeoutDuration = time.Duration(intTimeout) * time.Second
	}
	dialer = &kafka.Dialer{
		Timeout:   timeoutDuration,
		DualStack: true,
	}

	if kc.TLS != nil {
		tlsConfig, err := kc.NewTLSConfig()
		if err != nil {
			return dialer, err
		}
		dialer.TLS = tlsConfig
	} else if kc.SASL != nil && kc.SASL.UseSSL {
		// to use SSL to connect to the broker, the kafka-go module
		// simply needs an empty TLS Config
		tlsConfig := tls.Config{}
		dialer.TLS = &tlsConfig
	}

	if kc.SASL != nil {
		saslMechanism, err := kc.NewSASLConfig()
		if err != nil {
			return dialer, err
		}
		dialer.SASLMechanism = saslMechanism
	}
	return dialer, nil
}

func (kc *KafkaConfiguration) NewReader(dialer *kafka.Dialer, logger *log.Entry) (*kafka.Reader, error) {
	rConf := kafka.ReaderConfig{
		Brokers:        kc.Brokers,
		Topic:          kc.Topic,
		Dialer:         dialer,
		Logger:         kafka.LoggerFunc(logger.Debugf),
		ErrorLogger:    kafka.LoggerFunc(logger.Errorf),
		MinBytes:       2 * 10e5, // 2MB
		MaxBytes:       10e6,     // 10MB
		MaxWait:        10 * time.Second,
		CommitInterval: 3 * time.Second,
	}

	if kc.GroupID != "" && kc.Partition != 0 {
		return &kafka.Reader{}, errors.New("cannot specify both group_id and partition")
	}

	if kc.GroupID != "" {
		rConf.GroupID = kc.GroupID
	} else if kc.Partition != 0 {
		rConf.Partition = kc.Partition
	} else {
		logger.Warnf("no group_id specified, crowdsec will only read from the 1st partition of the topic")
	}

	if err := rConf.Validate(); err != nil {
		return &kafka.Reader{}, fmt.Errorf("while validating reader configuration: %w", err)
	}

	return kafka.NewReader(rConf), nil
}
