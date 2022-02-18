package web

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"

	trentoDB "github.com/trento-project/trento/internal/db"
	"github.com/trento-project/trento/internal/grafana"
	"github.com/trento-project/trento/version"
	"github.com/trento-project/trento/web/datapipeline"
	"github.com/trento-project/trento/web/entities"
	"github.com/trento-project/trento/web/models"
	"github.com/trento-project/trento/web/services"
	"github.com/trento-project/trento/web/telemetry"

	retryGo "github.com/avast/retry-go/v4"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	_ "github.com/trento-project/trento/docs/api" // docs is generated by Swag CLI, you have to import it.
)

//go:embed frontend/assets
var assetsFS embed.FS

//go:embed templates
var templatesFS embed.FS

var DBTables = []interface{}{
	&entities.Settings{}, &models.Tag{}, &models.SelectedChecks{}, &models.ConnectionSettings{},
	&entities.Check{}, &datapipeline.DataCollectedEvent{}, &datapipeline.Subscription{},
	&entities.HostTelemetry{}, &entities.Cluster{}, &entities.Host{}, &entities.HostHeartbeat{},
	&entities.SlesSubscription{}, &entities.SAPSystemInstance{}, &entities.ChecksResult{},
}

type App struct {
	InstallationID uuid.UUID
	config         *Config
	Dependencies
}

type Config struct {
	Host          string
	Port          int
	CollectorPort int
	EnablemTLS    bool
	Cert          string
	Key           string
	CA            string
	DBConfig      *trentoDB.Config
	GrafanaConfig *grafana.Config
}

type Dependencies struct {
	webEngine               *gin.Engine
	collectorEngine         *gin.Engine
	store                   cookie.Store
	projectorWorkersPool    *datapipeline.ProjectorsWorkerPool
	checksService           services.ChecksService
	subscriptionsService    services.SubscriptionsService
	tagsService             services.TagsService
	collectorService        services.CollectorService
	sapSystemsService       services.SAPSystemsService
	clustersService         services.ClustersService
	hostsService            services.HostsService
	settingsService         services.SettingsService
	healthSummaryService    services.HealthSummaryService
	telemetryRegistry       *telemetry.TelemetryRegistry
	telemetryPublisher      telemetry.Publisher
	premiumDetectionService services.PremiumDetectionService
	prometheusService       services.PrometheusService
}

func DefaultDependencies(config *Config) Dependencies {
	webEngine := NewNamedEngine("public")
	collectorEngine := NewNamedEngine("internal")
	store := cookie.NewStore([]byte("secret"))
	mode := os.Getenv(gin.EnvGinMode)

	gin.SetMode(mode)

	var db *gorm.DB
	err := retryGo.Do(
		func() error {
			var err error
			db, err = trentoDB.InitDB(config.DBConfig)
			if err != nil {
				return err
			}
			return nil
		},
		retryGo.Delay(1*time.Second),
		retryGo.MaxJitter(2*time.Second),
		retryGo.Attempts(8),
		retryGo.LastErrorOnly(true),
	)
	if err != nil {
		log.Fatalf("failed to connect database: %s", err)
	}

	if err := MigrateDB(db); err != nil {
		log.Fatalf("failed to migrate database: %s", err)
	}

	projectorRegistry := datapipeline.InitProjectorsRegistry(db)
	projectorWorkersPool := datapipeline.NewProjectorsWorkerPool(projectorRegistry)

	settingsService := services.NewSettingsService(db)
	tagsService := services.NewTagsService(db)
	subscriptionsService := services.NewSubscriptionsService(db)
	hostsService := services.NewHostsService(db)
	sapSystemsService := services.NewSAPSystemsService(db)
	premiumDetection := services.NewPremiumDetectionService(version.Flavor, subscriptionsService, settingsService)
	checksService := services.NewChecksService(db, premiumDetection)
	clustersService := services.NewClustersService(db, checksService)
	collectorService := services.NewCollectorService(db, projectorWorkersPool.GetChannel())
	telemetryRegistry := telemetry.NewTelemetryRegistry(db)
	telemetryPublisher := telemetry.NewTelemetryPublisher()
	prometheusService := services.NewPrometheusService(db)
	healthSummaryService := services.NewHealthSummaryService(sapSystemsService, clustersService, hostsService)

	return Dependencies{
		webEngine, collectorEngine, store, projectorWorkersPool,
		checksService, subscriptionsService, tagsService,
		collectorService, sapSystemsService, clustersService, hostsService, settingsService, healthSummaryService,
		telemetryRegistry, telemetryPublisher, premiumDetection, prometheusService,
	}
}

func NewNamedEngine(instance string) *gin.Engine {
	engine := gin.New()
	engine.Use(NewLogHandler(instance, log.StandardLogger()))
	engine.Use(gin.Recovery())
	return engine
}

func MigrateDB(db *gorm.DB) error {
	err := db.AutoMigrate(DBTables...)

	if err != nil {
		return err
	}

	return nil
}

// shortcut to use default dependencies
func NewApp(config *Config) (*App, error) {
	return NewAppWithDeps(config, DefaultDependencies(config))
}

func NewAppWithDeps(config *Config, deps Dependencies) (*App, error) {
	app := &App{
		config:       config,
		Dependencies: deps,
	}

	installationID, err := deps.settingsService.InitializeIdentifier()
	if err != nil {
		log.Errorf("failed to initialize installation identifier: %s", err)
		return nil, err
	}

	app.InstallationID = installationID

	InitAlerts()
	webEngine := deps.webEngine
	webEngine.HTMLRender = NewLayoutRender(templatesFS, "templates/*.tmpl")
	webEngine.Use(ErrorHandler)
	webEngine.Use(sessions.Sessions("session", deps.store))
	webEngine.StaticFS("/static", http.FS(assetsFS))
	webEngine.Use(EulaMiddleware(deps.premiumDetectionService))
	webEngine.GET("/", HomeHandler)
	webEngine.GET("/about", NewAboutHandler(deps.subscriptionsService))
	webEngine.GET("/eula", EulaShowHandler())
	webEngine.POST("/accept-eula", EulaAcceptHandler(deps.settingsService))
	webEngine.GET("/hosts", NewHostListHandler(deps.hostsService))
	webEngine.GET("/hosts/:id", NewHostHandler(deps.hostsService, deps.subscriptionsService, config.GrafanaConfig.URL))
	webEngine.GET("/catalog", NewChecksCatalogHandler(deps.checksService))
	webEngine.GET("/clusters", NewClusterListHandler(deps.clustersService))
	webEngine.GET("/clusters/:id", NewClusterHandler(deps.clustersService))
	webEngine.GET("/sapsystems", NewSAPSystemListHandler(deps.sapSystemsService))
	webEngine.GET("/sapsystems/:id", NewSAPResourceHandler(deps.hostsService, deps.sapSystemsService))
	webEngine.GET("/databases", NewHANADatabaseListHandler(deps.sapSystemsService))
	webEngine.GET("/databases/:id", NewSAPResourceHandler(deps.hostsService, deps.sapSystemsService))

	apiGroup := webEngine.Group("/api")
	{
		apiGroup.GET("/docs/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
		apiGroup.GET("/ping", ApiPingHandler)
		apiGroup.GET("/tags", ApiListTag(deps.tagsService))
		apiGroup.POST("/hosts/:id/tags", ApiHostCreateTagHandler(deps.hostsService, deps.tagsService))
		apiGroup.DELETE("/hosts/:id/tags/:tag", ApiHostDeleteTagHandler(deps.hostsService, deps.tagsService))
		apiGroup.POST("/clusters/:id/tags", ApiClusterCreateTagHandler(deps.clustersService, deps.tagsService))
		apiGroup.DELETE("/clusters/:id/tags/:tag", ApiClusterDeleteTagHandler(deps.clustersService, deps.tagsService))
		apiGroup.GET("/clusters/:cluster_id/results", ApiClusterCheckResultsHandler(deps.checksService))
		apiGroup.GET("/clusters/settings", ApiGetClustersSettingsHandler(deps.clustersService))
		apiGroup.POST("/sapsystems/:id/tags", ApiSAPSystemCreateTagHandler(deps.sapSystemsService, deps.tagsService))
		apiGroup.DELETE("/sapsystems/:id/tags/:tag", ApiSAPSystemDeleteTagHandler(deps.sapSystemsService, deps.tagsService))
		apiGroup.GET("/sapsystems/health", ApiSAPSystemsHealthSummaryHandler(deps.healthSummaryService))
		apiGroup.POST("/databases/:id/tags", ApiDatabaseCreateTagHandler(deps.sapSystemsService, deps.tagsService))
		apiGroup.DELETE("/databases/:id/tags/:tag", ApiDatabaseDeleteTagHandler(deps.sapSystemsService, deps.tagsService))
		apiGroup.GET("/checks/:id/settings", ApiCheckGetSettingsByIdHandler(deps.clustersService))
		apiGroup.POST("/checks/:id/settings", ApiCheckCreateSettingsByIdHandler(deps.checksService))
		apiGroup.PUT("/checks/catalog", ApiCreateChecksCatalogHandler(deps.checksService))
		apiGroup.GET("/checks/catalog", ApiChecksCatalogHandler(deps.checksService))
		apiGroup.POST("/checks/:id/results", ApiCreateChecksResultHandler(deps.checksService))
		apiGroup.GET("/prometheus/targets", ApiGetPrometheusHttpSdTargets(deps.prometheusService))
	}

	collectorEngine := deps.collectorEngine
	collectorEngine.POST("/api/collect", ApiCollectDataHandler(deps.collectorService))
	collectorEngine.POST("/api/hosts/:id/heartbeat", ApiHostHeartbeatHandler(deps.hostsService))
	collectorEngine.GET("/api/ping", ApiPingHandler)

	return app, nil
}

func (a *App) Start(ctx context.Context) error {
	grafana.InitGrafana(ctx, a.config.GrafanaConfig)

	webServer := &http.Server{
		Addr:           fmt.Sprintf("%s:%d", a.config.Host, a.config.Port),
		Handler:        a.webEngine,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	var tlsConfig *tls.Config
	var err error

	if a.config.EnablemTLS {
		tlsConfig, err = getTLSConfig(a.config.Cert, a.config.Key, a.config.CA)
		if err != nil {
			return err
		}
	}

	collectorServer := &http.Server{
		Addr:           fmt.Sprintf("%s:%d", a.config.Host, a.config.CollectorPort),
		Handler:        a.collectorEngine,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
		TLSConfig:      tlsConfig,
	}

	g, ctx := errgroup.WithContext(ctx)

	log.Info("Starting web server")
	g.Go(func() error {
		err := webServer.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	})

	log.Info("Starting collector server")
	g.Go(func() error {
		var err error
		if tlsConfig == nil {
			err = collectorServer.ListenAndServe()
		} else {
			err = collectorServer.ListenAndServeTLS("", "")
		}
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	})

	g.Go(func() error {
		a.projectorWorkersPool.Run(ctx)
		return nil
	})

	telemetryEngine := telemetry.NewEngine(
		a.InstallationID,
		a.Dependencies.telemetryPublisher,
		a.Dependencies.telemetryRegistry,
		a.Dependencies.premiumDetectionService,
	)

	g.Go(func() error {
		telemetryEngine.Start(ctx)
		return nil
	})

	go func() {
		<-ctx.Done()
		log.Info("Web server is shutting down.")
		webServer.Close()
		log.Info("Collector server is shutting down.")
		collectorServer.Close()
	}()

	return g.Wait()
}

func getTLSConfig(cert string, key string, ca string) (*tls.Config, error) {
	caCert, err := ioutil.ReadFile(ca)
	if err != nil {
		return nil, err
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)

	certificate, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		ClientCAs:    caCertPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		Certificates: []tls.Certificate{certificate},
	}, nil
}
