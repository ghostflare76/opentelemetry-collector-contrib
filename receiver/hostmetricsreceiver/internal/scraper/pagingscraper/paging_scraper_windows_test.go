// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build windows
// +build windows

package pagingscraper

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/receiver/scrapererror"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/hostmetricsreceiver/internal/perfcounters"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/hostmetricsreceiver/internal/scraper/pagingscraper/internal/metadata"
)

func TestScrape_Errors(t *testing.T) {
	type testCase struct {
		name                         string
		pageSize                     uint64
		getPageFileStats             func() ([]*pageFileStats, error)
		scrapeErr                    error
		getObjectErr                 error
		getValuesErr                 error
		expectedErr                  string
		expectedErrCount             int
		expectedUsedValue            int64
		expectedFreeValue            int64
		expectedUtilizationFreeValue float64
		expectedUtilizationUsedValue float64
	}

	testPageSize := uint64(4096)
	testPageFileData := &pageFileStats{usedBytes: 200, freeBytes: 800, totalBytes: 1000}

	testCases := []testCase{
		{
			name:     "standard",
			pageSize: testPageSize,
			getPageFileStats: func() ([]*pageFileStats, error) {
				return []*pageFileStats{testPageFileData}, nil
			},
			expectedUsedValue:            int64(testPageFileData.usedBytes),
			expectedFreeValue:            int64(testPageFileData.freeBytes),
			expectedUtilizationFreeValue: 0.8,
			expectedUtilizationUsedValue: 0.2,
		},
		{
			name:             "pageFileError",
			getPageFileStats: func() ([]*pageFileStats, error) { return nil, errors.New("err1") },
			expectedErr:      "failed to read page file stats: err1",
			expectedErrCount: pagingUsageMetricsLen,
		},
		{
			name:             "scrapeError",
			scrapeErr:        errors.New("err1"),
			expectedErr:      "err1",
			expectedErrCount: pagingMetricsLen,
		},
		{
			name:             "getObjectErr",
			getObjectErr:     errors.New("err1"),
			expectedErr:      "err1",
			expectedErrCount: pagingMetricsLen,
		},
		{
			name:             "getValuesErr",
			getValuesErr:     errors.New("err1"),
			expectedErr:      "err1",
			expectedErrCount: pagingMetricsLen,
		},
		{
			name:             "multipleErrors",
			getPageFileStats: func() ([]*pageFileStats, error) { return nil, errors.New("err1") },
			getObjectErr:     errors.New("err2"),
			expectedErr:      "failed to read page file stats: err1; err2",
			expectedErrCount: pagingUsageMetricsLen + pagingMetricsLen,
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			metricsConfig := metadata.DefaultMetricsSettings()
			metricsConfig.SystemPagingUtilization.Enabled = true

			scraper := newPagingScraper(context.Background(), componenttest.NewNopReceiverCreateSettings(), &Config{Metrics: metricsConfig})
			if test.getPageFileStats != nil {
				scraper.pageFileStats = test.getPageFileStats
			}

			pageSizeOnce.Do(func() {}) // Run this now so what we set pageSize to here is not overridden
			if test.pageSize > 0 {
				pageSize = test.pageSize
			} else {
				pageSize = getPageSize()
				assert.Greater(t, pageSize, uint64(0))
				assert.Zero(t, pageSize%4096) // page size on Windows should always be a multiple of 4KB
			}

			scraper.perfCounterScraper = perfcounters.NewMockPerfCounterScraperError(test.scrapeErr, test.getObjectErr, test.getValuesErr, nil)

			err := scraper.start(context.Background(), componenttest.NewNopHost())
			require.NoError(t, err, "Failed to initialize paging scraper: %v", err)

			md, err := scraper.scrape(context.Background())
			if test.expectedErr != "" {
				assert.EqualError(t, err, test.expectedErr)

				isPartial := scrapererror.IsPartialScrapeError(err)
				assert.True(t, isPartial)
				if isPartial {
					var scraperErr scrapererror.PartialScrapeError
					require.ErrorAs(t, err, &scraperErr)
					assert.Equal(t, test.expectedErrCount, scraperErr.Failed)
				}

				return
			}

			assert.NoError(t, err)

			metrics := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
			pagingUsageMetric := metrics.At(0)
			assert.Equal(t, test.expectedUsedValue, pagingUsageMetric.Sum().DataPoints().At(0).IntVal())
			assert.Equal(t, test.expectedFreeValue, pagingUsageMetric.Sum().DataPoints().At(1).IntVal())

			pagingUtilizationMetric := metrics.At(1)
			assert.Equal(t, test.expectedUtilizationUsedValue, pagingUtilizationMetric.Gauge().DataPoints().At(0).DoubleVal())
			assert.Equal(t, test.expectedUtilizationFreeValue, pagingUtilizationMetric.Gauge().DataPoints().At(1).DoubleVal())
		})
	}
}

func TestStart_Error(t *testing.T) {
	testCases := []struct {
		name               string
		initError          error
		expectedSkipScrape bool
	}{
		{
			name:               "Perfcounter partially fails to init",
			expectedSkipScrape: false,
		},
		{
			name: "Perfcounter fully fails to init",
			initError: &perfcounters.PerfCounterInitError{
				FailedObjects: []string{"Memory"},
			},
			expectedSkipScrape: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			metricsConfig := metadata.DefaultMetricsSettings()
			metricsConfig.SystemPagingUtilization.Enabled = true

			scraper := newPagingScraper(context.Background(), componenttest.NewNopReceiverCreateSettings(), &Config{Metrics: metricsConfig})

			scraper.perfCounterScraper = perfcounters.NewMockPerfCounterScraperError(nil, nil, nil, tc.initError)

			err := scraper.start(context.Background(), componenttest.NewNopHost())
			require.NoError(t, err, "Failed to initialize paging scraper: %v", err)

			require.Equal(t, tc.expectedSkipScrape, scraper.skipScrape)
		})
	}
}
