/* ****************************************************************************
 * Copyright 2020 51 Degrees Mobile Experts Limited
 *
 * Licensed under the Apache License, Version 2.0 (the "License"); you may not
 * use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
 * WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
 * License for the specific language governing permissions and limitations
 * under the License.
 * ***************************************************************************/

package swift

type volatile struct {
	common
}

func newVolatile() *volatile {
	var v volatile
	v.init()
	return &v
}

func (v volatile) getNode(domain string) (*node, error) {
	return v.common.getNode(domain)
}

func (v volatile) getNodes(network string) (*nodes, error) {
	return v.common.getNodes(network)
}

func (v volatile) setNode(n *node) error {
	var net *nodes
	v.nodes[n.domain] = n
	net = v.networks[n.network]
	if net == nil {
		net = newNodes()
		v.networks[n.network] = net
	}
	net.dict[n.domain] = n
	net.all = append(net.all, n)
	return nil
}
