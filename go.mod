module devenv-controller

go 1.13

require (
	github.com/Masterminds/semver v1.5.0 // indirect
	github.com/argoproj/argo-cd v1.3.6 // indirect
	github.com/argoproj/pkg v0.0.0-20200102163130-2dd1f3f6b4de // indirect
	github.com/casbin/casbin v1.9.1 // indirect
	github.com/crossplane/crossplane v0.9.0
	github.com/crossplane/crossplane-runtime v0.6.0
	github.com/crossplane/provider-gcp v0.7.0
	github.com/go-logr/logr v0.1.0
	github.com/gobuffalo/packr v1.30.1 // indirect
	github.com/gogo/protobuf v1.3.1 // indirect
	github.com/imdario/mergo v0.3.9 // indirect
	github.com/kanuahs/argo-cd v0.8.1-0.20191219202011-8883aacc9de6
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51 // indirect
	github.com/onsi/ginkgo v1.10.1
	github.com/onsi/gomega v1.7.0
	github.com/patrickmn/go-cache v2.1.0+incompatible // indirect
	github.com/robfig/cron v1.2.0 // indirect
	golang.org/x/crypto v0.0.0-20200108215511-5d647ca15757 // indirect
	golang.org/x/net v0.0.0-20191209160850-c0dbc17a3553 // indirect
	golang.org/x/sys v0.0.0-20200107162124-548cf772de50 // indirect
	golang.org/x/tools v0.0.0-20200108203644-89082a384178 // indirect
	golang.org/x/xerrors v0.0.0-20191204190536-9bdfabe68543 // indirect
	gopkg.in/src-d/go-git.v4 v4.13.1 // indirect
	k8s.io/api v0.17.3
	k8s.io/apimachinery v0.17.3
	k8s.io/client-go v0.17.3
	sigs.k8s.io/controller-runtime v0.4.0
)

// these fields have been replace'ed to fix cannot find module providing package xx error
replace github.com/spf13/pflag => github.com/spf13/pflag v1.0.3

replace golang.org/x/xerrors => golang.org/x/xerrors v0.0.0-20190717185122-a985d3407aa7
