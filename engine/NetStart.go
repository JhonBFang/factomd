// Copyright 2017 Factom Foundation
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

package engine

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io/ioutil"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/FactomProject/factomd/common/constants"
	"github.com/FactomProject/factomd/common/globals"
	"github.com/FactomProject/factomd/common/messages"
	"github.com/FactomProject/factomd/common/messages/electionMsgs"
	"github.com/FactomProject/factomd/common/messages/msgsupport"
	"github.com/FactomProject/factomd/common/primitives"
	"github.com/FactomProject/factomd/controlPanel"
	"github.com/FactomProject/factomd/database/databaseOverlay"
	"github.com/FactomProject/factomd/elections"
	"github.com/FactomProject/factomd/fnode"
	"github.com/FactomProject/factomd/p2p"
	"github.com/FactomProject/factomd/registry"
	"github.com/FactomProject/factomd/state"
	"github.com/FactomProject/factomd/util"
	"github.com/FactomProject/factomd/worker"
	"github.com/FactomProject/factomd/wsapi"

	llog "github.com/FactomProject/factomd/log"
	log "github.com/sirupsen/logrus"
)

var connectionMetricsChannel = make(chan interface{}, p2p.StandardChannelSize)
var mLog = new(MsgLog)
var p2pProxy *P2PProxy
var p2pNetwork *p2p.Controller
var logPort string

func init() {
	messages.General = new(msgsupport.GeneralFactory)
	primitives.General = messages.General
}

func echo(s string, more ...interface{}) {
	_, _ = os.Stderr.WriteString(fmt.Sprintf(s, more...))
}

func echoConfig(s *state.State, p *globals.FactomParams) {

	fmt.Println(">>>>>>>>>>>>>>>>")
	fmt.Println(">>>>>>>>>>>>>>>> Net Sim Start!")
	fmt.Println(">>>>>>>>>>>>>>>>")
	fmt.Println(">>>>>>>>>>>>>>>> Listening to Node", p.ListenTo)
	fmt.Println(">>>>>>>>>>>>>>>>")

	pnet := p.Net
	if len(p.Fnet) > 0 {
		pnet = p.Fnet
		p.Net = "file"
	}

	echo("%20s %s\n", "Build", Build)
	echo("%20s %s\n", "Node name", p.NodeName)
	echo("%20s %v\n", "balancehash", messages.AckBalanceHash)
	echo("%20s %s\n", fmt.Sprintf("%s Salt", s.GetFactomNodeName()), s.Salt.String()[:16])
	echo("%20s %v\n", "enablenet", p.EnableNet)
	echo("%20s %v\n", "net incoming", p2p.MaxNumberIncomingConnections)
	echo("%20s %v\n", "net outgoing", p2p.NumberPeersToConnect)
	echo("%20s %v\n", "waitentries", p.WaitEntries)
	echo("%20s %d\n", "node", p.ListenTo)
	echo("%20s %s\n", "prefix", p.Prefix)
	echo("%20s %d\n", "node count", p.Cnt)
	echo("%20s %d\n", "FastSaveRate", p.FastSaveRate)
	echo("%20s \"%s\"\n", "net spec", pnet)
	echo("%20s %d\n", "Msgs droped", p.DropRate)
	echo("%20s \"%s\"\n", "database", p.Db)
	echo("%20s \"%s\"\n", "database for clones", p.CloneDB)
	echo("%20s \"%s\"\n", "peers", p.Peers)
	echo("%20s \"%t\"\n", "exclusive", p.Exclusive)
	echo("%20s \"%t\"\n", "exclusive_in", p.ExclusiveIn)
	echo("%20s %d\n", "block time", p.BlkTime)
	echo("%20s %v\n", "runtimeLog", p.RuntimeLog)
	echo("%20s %v\n", "rotate", p.Rotate)
	echo("%20s %v\n", "timeOffset", p.TimeOffset)
	echo("%20s %v\n", "keepMismatch", p.KeepMismatch)
	echo("%20s %v\n", "startDelay", p.StartDelay)
	echo("%20s %v\n", "Network", s.Network)
	echo("%20s %x (%s)\n", "customnet", p.CustomNet, p.CustomNetName)
	echo("%20s %v\n", "deadline (ms)", p.Deadline)
	echo("%20s %v\n", "tls", s.FactomdTLSEnable)
	echo("%20s %v\n", "selfaddr", s.FactomdLocations)
	echo("%20s \"%s\"\n", "rpcuser", s.RpcUser)
	echo("%20s \"%s\"\n", "corsdomains", s.CorsDomains)
	echo("%20s %d\n", "Start 2nd Sync at ht", s.EntryDBHeightComplete)

	echo(fmt.Sprintf("%20s %d\n", "faultTimeout", elections.FaultTimeout))

	if "" == s.RpcPass {
		echo(fmt.Sprintf("%20s %s\n", "rpcpass", "is blank"))
	} else {
		echo(fmt.Sprintf("%20s %s\n", "rpcpass", "is set"))
	}
	echo("%20s \"%d\"\n", "TCP port", s.PortNumber)
	echo("%20s \"%s\"\n", "pprof port", logPort)
	echo("%20s \"%d\"\n", "Control Panel port", s.ControlPanelPort)
}

// init mlog & set log levels
func SetLogLevel(p *globals.FactomParams) {
	mLog.Init(p.RuntimeLog, p.Cnt)

	log.SetOutput(os.Stdout)
	switch strings.ToLower(p.Loglvl) {
	case "none":
		log.SetOutput(ioutil.Discard)
	case "debug":
		log.SetLevel(log.DebugLevel)
	case "info":
		log.SetLevel(log.InfoLevel)
	case "warning", "warn":
		log.SetLevel(log.WarnLevel)
	case "error":
		log.SetLevel(log.ErrorLevel)
	case "fatal":
		log.SetLevel(log.FatalLevel)
	case "panic":
		log.SetLevel(log.PanicLevel)
	}

	if p.Logjson {
		log.SetFormatter(&log.JSONFormatter{})
	}
}

// shutdown factomd
func interruptHandler() {
	fmt.Print("<Break>\n")
	fmt.Print("Gracefully shutting down the server...\n")
	for _, node := range fnode.GetFnodes() {
		node.State.ShutdownNode(0)
	}
	p2pNetwork.NetworkStop()
	close(registry.Exit)

	//fmt.Print("Waiting...\r\n")
	//time.Sleep(3 * time.Second)
	// TODO: refactor to shut down all threads
	//os.Exit(0)
}

func initEntryHeight(s *state.State, target int) {
	if target >= 0 {
		s.EntryDBHeightComplete = uint32(target)
		s.LogPrintf("EntrySync", "Force with Sync2 NetStart EntryDBHeightComplete = %d", s.EntryDBHeightComplete)
	} else {
		height, err := s.DB.FetchDatabaseEntryHeight()
		if err != nil {
			s.LogPrintf("EntrySync", "Error reading EntryDBHeightComplete NetStart EntryDBHeightComplete = %d", s.EntryDBHeightComplete)
			_, _ = os.Stderr.WriteString(fmt.Sprintf("ERROR reading Entry DBHeight Complete: %v\n", err))
		} else {
			s.EntryDBHeightComplete = height
			s.LogPrintf("EntrySync", "NetStart EntryDBHeightComplete = %d", s.EntryDBHeightComplete)
		}
	}
}

func NetStart(w *worker.Thread, p *globals.FactomParams, listenToStdin bool) {
	initEngine(w, p)
	for i := 0; i < p.Cnt; i++ {
		fnode.Factory(w)
	}
	startNetwork(w, p)
	startFnodes(w)
	startWebserver(w)
	startSimControl(w, p.ListenTo, listenToStdin)
}

// initialize package-level vars
func initEngine(w *worker.Thread, p *globals.FactomParams) {
	messages.AckBalanceHash = p.AckbalanceHash
	w.RegisterInterruptHandler(interruptHandler)

	// nodes can spawn with a different thread lifecycle
	fnode.Factory = func(w *worker.Thread) {
		makeServer(w, p)
	}
}

// Anchoring related configurations
func initAnchors(s *state.State, reparse bool) {
	config := s.Cfg.(*util.FactomdConfig)
	if len(config.App.BitcoinAnchorRecordPublicKeys) > 0 {
		err := s.GetDB().(*databaseOverlay.Overlay).SetBitcoinAnchorRecordPublicKeysFromHex(config.App.BitcoinAnchorRecordPublicKeys)
		if err != nil {
			panic("Encountered an error while trying to set custom Bitcoin anchor record keys from config")
		}
	}
	if len(config.App.EthereumAnchorRecordPublicKeys) > 0 {
		err := s.GetDB().(*databaseOverlay.Overlay).SetEthereumAnchorRecordPublicKeysFromHex(config.App.EthereumAnchorRecordPublicKeys)
		if err != nil {
			panic("Encountered an error while trying to set custom Ethereum anchor record keys from config")
		}
	}
	if reparse {
		fmt.Println("Reparsing anchor chains...")
		err := s.GetDB().(*databaseOverlay.Overlay).ReparseAnchorChains()
		if err != nil {
			panic("Encountered an error while trying to re-parse anchor chains: " + err.Error())
		}
	}
}

// construct a simulated network
func buildNetTopology(p *globals.FactomParams) {
	nodes := fnode.GetFnodes()

	switch p.Net {
	case "file":
		file, err := os.Open(p.Fnet)
		if err != nil {
			panic(fmt.Sprintf("File network.txt failed to open: %s", err.Error()))
		} else if file == nil {
			panic(fmt.Sprint("File network.txt failed to open, and we got a file of <nil>"))
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			var a, b int
			var s string
			_, _ = fmt.Sscanf(scanner.Text(), "%d %s %d", &a, &s, &b)
			if s == "--" {
				AddSimPeer(nodes, a, b)
			}
		}
	case "square":
		side := int(math.Sqrt(float64(p.Cnt)))

		for i := 0; i < side; i++ {
			AddSimPeer(nodes, i*side, (i+1)*side-1)
			AddSimPeer(nodes, i, side*(side-1)+i)
			for j := 0; j < side; j++ {
				if j < side-1 {
					AddSimPeer(nodes, i*side+j, i*side+j+1)
				}
				AddSimPeer(nodes, i*side+j, ((i+1)*side)+j)
			}
		}
	case "long":
		fmt.Println("Using long Network")
		for i := 1; i < p.Cnt; i++ {
			AddSimPeer(nodes, i-1, i)
		}
		// Make long into a circle
	case "loops":
		fmt.Println("Using loops Network")
		for i := 1; i < p.Cnt; i++ {
			AddSimPeer(nodes, i-1, i)
		}
		for i := 0; (i+17)*2 < p.Cnt; i += 17 {
			AddSimPeer(nodes, i%p.Cnt, (i+5)%p.Cnt)
		}
		for i := 0; (i+13)*2 < p.Cnt; i += 13 {
			AddSimPeer(nodes, i%p.Cnt, (i+7)%p.Cnt)
		}
	case "alot":
		n := len(nodes)
		for i := 0; i < n; i++ {
			AddSimPeer(nodes, i, (i+1)%n)
			AddSimPeer(nodes, i, (i+5)%n)
			AddSimPeer(nodes, i, (i+7)%n)
		}

	case "alot+":
		n := len(nodes)
		for i := 0; i < n; i++ {
			AddSimPeer(nodes, i, (i+1)%n)
			AddSimPeer(nodes, i, (i+5)%n)
			AddSimPeer(nodes, i, (i+7)%n)
			AddSimPeer(nodes, i, (i+13)%n)
		}

	case "tree":
		index := 0
		row := 1
	treeloop:
		for i := 0; true; i++ {
			for j := 0; j <= i; j++ {
				AddSimPeer(nodes, index, row)
				AddSimPeer(nodes, index, row+1)
				row++
				index++
				if index >= len(nodes) {
					break treeloop
				}
			}
			row += 1
		}
	case "circles":
		circleSize := 7
		index := 0
		for {
			AddSimPeer(nodes, index, index+circleSize-1)
			for i := index; i < index+circleSize-1; i++ {
				AddSimPeer(nodes, i, i+1)
			}
			index += circleSize

			AddSimPeer(nodes, index, index-circleSize/3)
			AddSimPeer(nodes, index+2, index-circleSize-circleSize*2/3-1)
			AddSimPeer(nodes, index+3, index-(2*circleSize)-circleSize*2/3)
			AddSimPeer(nodes, index+5, index-(3*circleSize)-circleSize*2/3+1)

			if index >= len(nodes) {
				break
			}
		}
	default:
		fmt.Println("Didn't understand network type. Known types: mesh, long, circles, tree, loops.  Using a Long Network")
		for i := 1; i < p.Cnt; i++ {
			AddSimPeer(nodes, i-1, i)
		}

	}

	var colors = []string{"95cde5", "b01700", "db8e3c", "ffe35f"}

	if len(nodes) > 2 {
		for i, s := range nodes {
			fmt.Printf("%d {color:#%v, shape:dot, label:%v}\n", i, colors[i%len(colors)], s.State.FactomNodeName)
		}
		fmt.Printf("Paste the network info above into http://arborjs.org/halfviz to visualize the network\n")
	}
}

func startWebserver(w *worker.Thread) {
	state0 := fnode.Get(0).State
	wsapi.Start(w, state0)
	if state0.DebugExec() && llog.CheckFileName("graphData.txt") {
		go printGraphData("graphData.txt", 30)
	}

	// Start prometheus on port
	launchPrometheus(9876)

	w.Run(func() {
		controlPanel.ServeControlPanel(state0.ControlPanelChannel, state0, connectionMetricsChannel, p2pNetwork, Build, state0.FactomNodeName)
	})
}

func startNetwork(w *worker.Thread, p *globals.FactomParams) {
	s := fnode.Get(0).State

	// Modify Identities of simulated nodes
	if fnode.Len() > 1 && len(s.Prefix) == 0 {
		modifySimulatorIdentities() // set proper chain id & keys
	}

	// Start the P2P network
	var networkID p2p.NetworkID
	var seedURL, networkPort, configPeers string
	switch s.Network {
	case "MAIN", "main":
		networkID = p2p.MainNet
		seedURL = s.MainSeedURL
		networkPort = s.MainNetworkPort
		configPeers = s.MainSpecialPeers
		s.DirectoryBlockInSeconds = 600
	case "TEST", "test":
		networkID = p2p.TestNet
		seedURL = s.TestSeedURL
		networkPort = s.TestNetworkPort
		configPeers = s.TestSpecialPeers
	case "LOCAL", "local":
		networkID = p2p.LocalNet
		seedURL = s.LocalSeedURL
		networkPort = s.LocalNetworkPort
		configPeers = s.LocalSpecialPeers

		// Also update the local constants for custom networks
		fmt.Println("Running on the local network, use local coinbase constants")
		constants.SetLocalCoinBaseConstants()
	case "CUSTOM", "custom":
		if bytes.Compare(p.CustomNet, []byte("\xe3\xb0\xc4\x42")) == 0 {
			panic("Please specify a custom network with -customnet=<something unique here>")
		}
		s.CustomNetworkID = p.CustomNet
		networkID = p2p.NetworkID(binary.BigEndian.Uint32(p.CustomNet))
		for _, node := range fnode.GetFnodes() {
			node.State.CustomNetworkID = p.CustomNet
		}
		seedURL = s.CustomSeedURL
		networkPort = s.CustomNetworkPort
		configPeers = s.CustomSpecialPeers

		// Also update the coinbase constants for custom networks
		fmt.Println("Running on the custom network, use custom coinbase constants")
		constants.SetCustomCoinBaseConstants()
	default:
		panic("Invalid Network choice in Config File or command line. Choose MAIN, TEST, LOCAL, or CUSTOM")
	}

	p2p.NetworkDeadline = time.Duration(p.Deadline) * time.Millisecond
	buildNetTopology(p)

	if !p.EnableNet {
		return
	}

	if 0 < p.NetworkPortOverride {
		networkPort = fmt.Sprintf("%d", p.NetworkPortOverride)
	}

	ci := p2p.ControllerInit{
		NodeName:                 s.FactomNodeName,
		Port:                     networkPort,
		PeersFile:                s.PeersFile,
		Network:                  networkID,
		Exclusive:                p.Exclusive,
		ExclusiveIn:              p.ExclusiveIn,
		SeedURL:                  seedURL,
		ConfigPeers:              configPeers,
		CmdLinePeers:             p.Peers,
		ConnectionMetricsChannel: connectionMetricsChannel,
	}

	p2pNetwork = new(p2p.Controller).Initialize(ci)
	s.NetworkController = p2pNetwork
	p2pNetwork.Init(s, "p2pNetwork")
	p2pNetwork.StartNetwork(w)

	p2pProxy = new(P2PProxy).Initialize(s.FactomNodeName, "P2P Network").(*P2PProxy)
	p2pProxy.Init(s, "p2pProxy")
	p2pProxy.FromNetwork = p2pNetwork.FromNetwork
	p2pProxy.ToNetwork = p2pNetwork.ToNetwork
	p2pProxy.StartProxy(w)
}

func printGraphData(filename string, period int) {
	downscale := int64(1)
	llog.LogPrintf(filename, "\t%9s\t%9s\t%9s\t%9s\t%9s\t%9s", "Dbh-:-min", "Node", "ProcessCnt", "ListPCnt", "UpdateState", "SleepCnt")
	for {
		for _, f := range fnode.GetFnodes() {
			s := f.State
			llog.LogPrintf(filename, "\t%9s\t%9s\t%9d\t%9d\t%9d\t%9d", fmt.Sprintf("%d-:-%d", s.LLeaderHeight, s.CurrentMinute), s.FactomNodeName, s.StateProcessCnt/downscale, s.ProcessListProcessCnt/downscale, s.StateUpdateState/downscale, s.ValidatorLoopSleepCnt/downscale)
		}
		time.Sleep(time.Duration(period) * time.Second)
	} // for ever ...
}

var state0Init sync.Once // we do some extra init for the first state

//**********************************************************************
// Functions that access variables in this method to set up Factom Nodes
// and start the servers.
//**********************************************************************
func makeServer(w *worker.Thread, p *globals.FactomParams) (node *fnode.FactomNode) {
	i := fnode.Len()

	if i == 0 {
		node = fnode.New(state.NewState(p, FactomdVersion))
	} else {
		node = fnode.New(state.Clone(fnode.Get(0).State, i).(*state.State))
	}
	node.State.Initialize(w)

	state0Init.Do(func() {
		logPort = p.LogPort
		SetLogLevel(p)
		setupFirstAuthority(node.State)
		initEntryHeight(node.State, p.Sync2)
		initAnchors(node.State, p.ReparseAnchorChains)
		echoConfig(node.State, p) // print the config only once
	})

	node.State.EFactory = new(electionMsgs.ElectionsFactory)
	time.Sleep(10 * time.Millisecond)

	return node
}

func startFnodes(w *worker.Thread) {
	for _, node := range fnode.GetFnodes() {
		startServer(w, node)
	}
}

func startServer(w *worker.Thread, node *fnode.FactomNode) {
	NetworkProcessorNet(w, node)
	node.State.ValidatorLoop(w)
	elections.Run(w, node.State)
	node.State.StartMMR(w)

	w.Run(func() { state.LoadDatabase(node.State) })
	w.Run(node.State.GoSyncEntries)
	w.Run(func() { Timer(node.State) })
	w.Run(node.State.MissingMessageResponseHandler.Run)
}

func setupFirstAuthority(s *state.State) {
	if len(s.IdentityControl.Authorities) > 0 {
		//Don't initialize first authority if we are loading during fast boot
		//And there are already authorities present
		return
	}

	_ = s.IdentityControl.SetBootstrapIdentity(s.GetNetworkBootStrapIdentity(), s.GetNetworkBootStrapKey())
}

// create a new simulated fnode
func AddNode() {
	p := registry.New()
	p.Register(func(w *worker.Thread) {
		i := fnode.Len()
		fnode.Factory(w)
		modifySimulatorIdentity(i)
		AddSimPeer(fnode.GetFnodes(), i, i-1) // KLUDGE peer w/ only last node
		n := fnode.Get(i)
		startServer(w, n)
	})
	go p.Run()
	p.WaitForRunning()
}
