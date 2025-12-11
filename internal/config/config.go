// Package config handles environment variable configuration loading.
package config

import (
	"errors"
	"os"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	// ServiceNow connection settings
	ServiceNowBaseURL      string
	ServiceNowEndpointPath string
	ServiceNowUsername     string
	ServiceNowPassword     string

	// ServiceNow incident field defaults
	ServiceNowCategory        string
	ServiceNowSubcategory     string
	ServiceNowAssignmentGroup string
	ServiceNowCallerID        string
	ServiceNowRootCause       string
	ServiceNowUrgency         string
	ServiceNowImpact          string

	// HTTP server settings
	HTTPPort string

	// Label key configuration for alert processing
	ClusterLabelKey     string
	EnvironmentLabelKey string
}

// Load reads configuration from environment variables and returns a Config.
// Returns an error if required fields are missing.
func Load() (*Config, error) {
	cfg := &Config{
		ServiceNowBaseURL:         os.Getenv("SERVICENOW_BASE_URL"),
		ServiceNowEndpointPath:    getEnvOrDefault("SERVICENOW_ENDPOINT_PATH", "/api/now/table/incident"),
		ServiceNowUsername:        os.Getenv("SERVICENOW_USERNAME"),
		ServiceNowPassword:        os.Getenv("SERVICENOW_PASSWORD"),
		ServiceNowCategory:        getEnvOrDefault("SERVICENOW_CATEGORY", "software"),
		ServiceNowSubcategory:     getEnvOrDefault("SERVICENOW_SUBCATEGORY", "openshift"),
		ServiceNowAssignmentGroup: os.Getenv("SERVICENOW_ASSIGNMENT_GROUP"), // Optional, empty if not set
		ServiceNowCallerID:        os.Getenv("SERVICENOW_CALLER_ID"),        // Optional, empty if not set
		ServiceNowRootCause:       getEnvOrDefault("SERVICENOW_ROOT_CAUSE", "Environmental"),
		ServiceNowUrgency:         getEnvOrDefault("SERVICENOW_URGENCY", "3"),
		ServiceNowImpact:          getEnvOrDefault("SERVICENOW_IMPACT", "3"),
		HTTPPort:                  getEnvOrDefault("HTTP_PORT", "8080"),
		ClusterLabelKey:           getEnvOrDefault("CLUSTER_LABEL_KEY", "cluster"),
		EnvironmentLabelKey:       getEnvOrDefault("ENVIRONMENT_LABEL_KEY", "environment"),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// validate checks that all required configuration fields are present.
func (c *Config) validate() error {
	if c.ServiceNowBaseURL == "" {
		return errors.New("SERVICENOW_BASE_URL is required")
	}
	if c.ServiceNowUsername == "" {
		return errors.New("SERVICENOW_USERNAME is required")
	}
	if c.ServiceNowPassword == "" {
		return errors.New("SERVICENOW_PASSWORD is required")
	}
	return nil
}

// getEnvOrDefault returns the environment variable value or a default if not set.
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
