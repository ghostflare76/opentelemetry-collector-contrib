// Copyright  The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ottlcommon // import "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/oteltransformationlanguage/functions/ottlcommon"

import "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/oteltransformationlanguage/ottl"

func Set(target ottl.Setter, value ottl.Getter) (ottl.ExprFunc, error) {
	return func(ctx ottl.TransformContext) interface{} {
		val := value.Get(ctx)

		// No fields currently support `null` as a valid type.
		if val != nil {
			target.Set(ctx, val)
		}
		return nil
	}, nil
}
