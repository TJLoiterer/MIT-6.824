package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import (
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"time"
)
import "sync/atomic"
import "labrpc"

// import "bytes"
// import "../labgob"

//
// as each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make(). set
// CommandValid to true to indicate that the ApplyMsg contains a newly
// committed log entry.
//
// in Lab 3 you'll want to send other kinds of messages (e.g.,
// snapshots) on the applyCh; at that point you can add fields to
// ApplyMsg, but set CommandValid to false for these other uses.
//
type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int
}

//
// A Go object implementing a single Raft peer.
//
const (
	LeaderState    = 0
	FollowerState  = 1
	CandidateState = 2
)

type Raft struct {
	mu        sync.Mutex          // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	dead      int32               // set by Kill()

	// Your data here (2A, 2B, 2C).
	// Look at the paper's Figure 2 for a description of what
	// state a Raft server must maintain.

	myState       int
	followerCond  *sync.Cond
	leaderCond    *sync.Cond
	candidateCond *sync.Cond

	currentTerm int
	votedFor    int

	// logs
	commitIndex int
	lastApplied int
	nextIndex   int
	matchIndex  int

	// wait duration parameters in ms
	electWaitDuration     int
	heartBeatWaitDuration int
	timerCleared          bool

	// heartbeat response
	appendEntriesChan chan *AsyncRpcInfo
	requestVoteChan   chan *AsyncRpcInfo
}

// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {
	// Your code here (2A).

	return rf.currentTerm, rf.myState == LeaderState
}

//
// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
//
func (rf *Raft) persist() {
	// Your code here (2C).
	// Example:
	// w := new(bytes.Buffer)
	// e := labgob.NewEncoder(w)
	// e.Encode(rf.xxx)
	// e.Encode(rf.yyy)
	// data := w.Bytes()
	// rf.persister.SaveRaftState(data)
}

//
// restore previously persisted state.
//
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	// Your code here (2C).
	// Example:
	// r := bytes.NewBuffer(data)
	// d := labgob.NewDecoder(r)
	// var xxx
	// var yyy
	// if d.Decode(&xxx) != nil ||
	//    d.Decode(&yyy) != nil {
	//   error...
	// } else {
	//   rf.xxx = xxx
	//   rf.yyy = yyy
	// }
}

func (rf *Raft) setState(state int) {
	rf.myState = state
}

func (rf *Raft) followPeer(peerId int) {
	rf.myState = FollowerState
	rf.votedFor = peerId
	rf.timerCleared = true
}

func (rf *Raft) tryFollowNewerTerm(candidateId int, newTerm int) bool {
	if rf.currentTerm < newTerm {
		fmt.Println(rf.PrefixPrint(), "sees newer term", newTerm, "from peer", candidateId)
		rf.currentTerm = newTerm
		rf.followPeer(candidateId)
		return true
	}
	return false
}

func (rf *Raft) tryDiscardOldTerm(peerId int, oldTerm int) bool {
	if rf.currentTerm > oldTerm {
		fmt.Println(rf.PrefixPrint(), "received args from peer", peerId, " of term", oldTerm, ", discarding")
		return true
	}
	return false
}

func (rf *Raft) selfPrint() string {
	return "Peer " + strconv.Itoa(rf.me)
}

func (rf *Raft) statePrint() string {
	switch rf.myState {
	case LeaderState:
		return "Leader"
	case FollowerState:
		return "Follower"
	case CandidateState:
		return "Candidate"
	}
	return "Unknown"
}

func (rf *Raft) termPrint() string {
	return strconv.Itoa(rf.currentTerm)
}

func (rf *Raft) PrefixPrint() string {
	return "[" + rf.selfPrint() + " " + rf.statePrint() + " term " + rf.termPrint() + "]"
}

func (rf *Raft) callAsync(callback interface{}, peerId int, rpcName string, args interface{}, reply interface{}, xargs ...interface{}) {
	go func() {
		ok := rf.peers[peerId].Call(rpcName, args, reply)
		callback.(func(*Raft, bool, int, interface{}, interface{}, ...interface{}))(rf, ok, peerId, args, reply, xargs)
	}()
}

//
// example RequestVote RPC arguments structure.
// field names must start with capital letters!
//
type RequestVoteArgs struct {
	// Your data here (2A, 2B).
	Term         int
	CandidateId  int
	LastLogIndex int
	LastLogTerm  int
}

//
// example RequestVote RPC reply structure.
// field names must start with capital letters!
//
type RequestVoteReply struct {
	// Your data here (2A).
	GrantVote bool
	Term      int
}

type AppendEntriesArgs struct {
	// machine state
	Term     int
	LeaderId int
	// log state
	PrevLogIndex int
	PrevLogTerm  int
	//entries
	LeaderCommit int
}

type AppendEntriesReply struct {
	Term    int
	Success bool
}

//
// example RequestVote RPC handler.
//
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (2A, 2B).

	//rf.mu.Lock()
	// default reply state
	reply.Term = rf.currentTerm
	reply.GrantVote = false

	if rf.tryDiscardOldTerm(args.CandidateId, args.Term) {
		reply.Term = rf.currentTerm
		reply.GrantVote = false
		return
	}
	if rf.tryFollowNewerTerm(args.CandidateId, args.Term) {
		reply.GrantVote = true
		rf.timerCleared = true
		return
	}
	//if args.Term > rf.currentTerm {
	//	// received vote term larger than that of itself
	//	// must follow
	//	fmt.Printf("Peer %v at term %v received vote with more recent term %v from peer %v, following\n", rf.me, rf.currentTerm, args.Term, args.CandidateId)
	//	reply.GrantVote = true
	//	rf.timerCleared = true
	//	rf.myState = FollowerState
	//	rf.votedFor = args.CandidateId
	//	rf.currentTerm = args.Term
	//}
	// < 0 for not elected leader
	// == for already accepted leader
	if rf.votedFor < 0 || rf.votedFor == args.CandidateId {
		if rf.commitIndex >= args.LastLogIndex {
			reply.GrantVote = true
			rf.timerCleared = true
			fmt.Println(rf.PrefixPrint(), "granting vote to peer", args.CandidateId, "at term", args.Term)
			//rf.mu.Unlock()
			return
		}
	}
	fmt.Println(rf.PrefixPrint(), "with leader", rf.votedFor, "at term %v not granting vote to peer", rf.currentTerm, "at term", args.Term)
	//rf.mu.Unlock()
}

// A non-leader should receive this
func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	fmt.Println(rf.PrefixPrint(), "got heartbeat message from leader peer", args.LeaderId, "at term", args.Term)
	//rf.mu.Lock()
	// default reply state
	reply.Term = rf.currentTerm
	reply.Success = true

	if rf.tryDiscardOldTerm(args.LeaderId, args.Term) {
		reply.Success = false
		return
	}
	if rf.tryFollowNewerTerm(args.LeaderId, args.Term) {
		reply.Success = true
		rf.timerCleared = true
		return
	}
	switch rf.myState {
	case LeaderState:
		break
	case FollowerState:
		rf.timerCleared = true
		break
	case CandidateState:
		rf.myState = FollowerState
		rf.currentTerm = args.Term
		rf.votedFor = args.LeaderId
		rf.timerCleared = true
		reply.Success = true
		fmt.Println(rf.PrefixPrint(), "set leader to peer", args.LeaderId, "by heartbeat message")
		break
	default:
		panic("Invalid peer state in rpc AppendEntries!")
	}

	//rf.mu.Unlock()
}

//
// example code to send a RequestVote RPC to a server.
// server is the index of the target server in rf.peers[].
// expects RPC arguments in args.
// fills in *reply with RPC reply, so caller should
// pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// The labrpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost.
// Call() sends a request and waits for a reply. If a reply arrives
// within a timeout interval, Call() returns true; otherwise
// Call() returns false. Thus Call() may not return for a while.
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply.
//
// Call() is guaranteed to return (perhaps after a delay) *except* if the
// handler function on the server side does not return.  Thus there
// is no need to implement your own timeouts around Call().
//
// look at the comments in ../labrpc/labrpc.go for more details.
//
// if you're having trouble getting RPC to work, check that you've
// capitalized all field names in structs passed over RPC, and
// that the caller passes the address of the reply struct with &, not
// the struct itself.
//
func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}

func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)
	return ok
}

//
// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election. even if the Raft instance has been killed,
// this function should return gracefully.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// Term. the third return value is true if this server believes it is
// the leader.
//
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	index := -1
	term := -1
	isLeader := true

	// Your code here (2B).

	return index, term, isLeader
}

//
// the tester doesn't halt goroutines created by Raft after each test,
// but it does call the Kill() method. your code can use killed() to
// check whether Kill() has been called. the use of atomic avoids the
// need for a lock.
//
// the issue is that long-running goroutines use memory and may chew
// up CPU time, perhaps causing later tests to fail and generating
// confusing debug output. any goroutine with a long-running loop
// should call killed() to check whether it should stop.
//
func (rf *Raft) Kill() {
	atomic.StoreInt32(&rf.dead, 1)
	// Your code here, if desired.
	fmt.Println(rf.PrefixPrint(), "killed by an outer caller")
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

/*
func (rf *Raft) runElect() {
	// must wait for a little while
	if rf.myState != CandidateState {
		return
	}
	maxRandTime := 500
	minRandTime := 0
	randTime := rand.Intn(maxRandTime - minRandTime) + minRandTime
	//if randTime > (maxRandTime + minRandTime) / 2 {
	//	maxRandTime = randTime
	//} else {
	//	minRandTime = randTime
	//}
	fmt.Println(rf.PrefixPrint(), "running as Candidate electing in", randTime, "ms")
	//fmt.Printf("Peer %v running as Candidate electing", rf.me)
	time.Sleep(time.Duration(randTime) * time.Millisecond)
	if rf.myState != CandidateState {
		return
	}

	rf.currentTerm++
	//rf.votedFor = rf.me

	fmt.Println(rf.PrefixPrint(), "issuing election")

	//rf.mu.Lock()

	args := &RequestVoteArgs{
		Term:         rf.currentTerm,
		CandidateId:  rf.me,
		LastLogIndex: rf.commitIndex,
		LastLogTerm:  0,
	}

	//rf.mu.Unlock()

	replyArray := make([]RequestVoteReply, len(rf.peers))
	aliveCount := 0
	voteCount := 0
	for peerIndex, _ := range rf.peers {
		if peerIndex == rf.me {
			// does not send to myself
			continue
		}
		if rf.myState != CandidateState {
			return
		}
		fmt.Println(rf.PrefixPrint(), "requesting vote from peer", peerIndex)
		if rf.sendRequestVote(peerIndex, args, &replyArray[peerIndex]) {
			aliveCount++
			if rf.tryFollowNewerTerm(peerIndex, replyArray[peerIndex].Term) {
				return
			}
			if replyArray[peerIndex].GrantVote {
				fmt.Println(rf.PrefixPrint(), "granted a vote by peer", peerIndex)
				voteCount++
			}
		} else {
			fmt.Println(rf.PrefixPrint(), "cannot reach peer", peerIndex, "when requesting a vote")
		}
	}
	// account for itself
	aliveCount++
	voteCount++
	if rf.myState != CandidateState {
		return
	}
	fmt.Println(rf.PrefixPrint(), "got", voteCount, "votes in", aliveCount, "alive peers")
	if voteCount > aliveCount / 2 && aliveCount != 1 {
		fmt.Println(rf.PrefixPrint(), "elected leader at term", rf.currentTerm)
		// leader claimed!
		//rf.mu.Lock()
		rf.myState = LeaderState
		rf.votedFor = rf.me
		//rf.mu.Unlock()
		return
	}
	// stay as candidate for the next election or other's vote
	//rf.mu.Lock()
	rf.myState = CandidateState
	rf.votedFor = -1


	//rf.mu.Unlock()
}
*/

type AsyncRpcInfo struct {
	AliveCount   int
	SuccessCount int
	TotalCount   int
	CurrentCount int

	Cond     *sync.Cond
	MustExit bool
}

func (ri *AsyncRpcInfo) IncrementAliveCount() {
	ri.Cond.L.Lock()
	if !ri.MustExit {
		ri.AliveCount++
	}
	ri.Cond.L.Unlock()
}

func (ri *AsyncRpcInfo) IncrementSuccessCount() {
	ri.Cond.L.Lock()
	if !ri.MustExit {
		ri.SuccessCount++
	}
	ri.Cond.L.Unlock()
}

func (ri *AsyncRpcInfo) IncrementCurrentCount() {
	ri.Cond.L.Lock()
	if !ri.MustExit {
		ri.CurrentCount++
	}
	ri.Cond.L.Unlock()
	if ri.CurrentCount >= ri.TotalCount {
		ri.Cond.Broadcast()
	}
}

func (ri *AsyncRpcInfo) Wait() {
	ri.Cond.L.Lock()
	for ri.CurrentCount < ri.TotalCount && !ri.MustExit {
		ri.Cond.Wait()
	}
	ri.Cond.L.Unlock()
}

func (ri *AsyncRpcInfo) SetMustExit() {
	ri.Cond.L.Lock()
	ri.MustExit = true
	ri.Cond.L.Unlock()
	ri.Cond.Broadcast()
}

func (rf *Raft) newAsyncRpcInfo(total int) *AsyncRpcInfo {
	return &AsyncRpcInfo{
		TotalCount: total,
		Cond:       sync.NewCond(&sync.Mutex{}),
	}
}

func (rf *Raft) requestVoteCallBack(ok bool, peerIndex int, args *RequestVoteArgs, reply *RequestVoteReply, requestVoteInfo *AsyncRpcInfo) {
	if !ok {
		fmt.Println(rf.PrefixPrint(), "cannot reach peer", peerIndex, "when requesting a vote")
	} else {
		requestVoteInfo.IncrementAliveCount()
		if rf.tryFollowNewerTerm(peerIndex, reply.Term) {
			requestVoteInfo.SetMustExit()
		} else if reply.GrantVote {
			fmt.Println(rf.PrefixPrint(), "granted a vote by peer", peerIndex)
			requestVoteInfo.IncrementSuccessCount()
			if requestVoteInfo.SuccessCount+1 > requestVoteInfo.TotalCount/2 {
				// leader claimed!
				fmt.Println(rf.PrefixPrint(), "got", requestVoteInfo.SuccessCount, "votes in", requestVoteInfo.AliveCount, "alive peers", requestVoteInfo.TotalCount, "total peers")
				rf.myState = LeaderState
				rf.votedFor = rf.me
				fmt.Println(rf.PrefixPrint(), "elected leader at term", rf.currentTerm)
				requestVoteInfo.SetMustExit()
			}
		}
	}
	requestVoteInfo.IncrementCurrentCount()
}

func (rf *Raft) sendRequestVoteAsync(
	peerIndex int,
	args *RequestVoteArgs,
	reply *RequestVoteReply,
	info *AsyncRpcInfo) {
	go func() {
		if info.MustExit {
			return
		}
		sendBegin := time.Now()
		ok := rf.sendRequestVote(peerIndex, args, reply)
		sendEnd := time.Now()
		fmt.Println(rf.PrefixPrint(), "vote response from peer", peerIndex, "received in", sendEnd.Sub(sendBegin))
		rf.requestVoteCallBack(ok, peerIndex, args, reply, info)
	}()
}

func (rf *Raft) runElect() {
	// must wait for a little while
	if rf.myState != CandidateState {
		return
	}
	maxRandTime := 800
	minRandTime := 0
	randTime := rand.Intn(maxRandTime-minRandTime) + minRandTime
	//if randTime > (maxRandTime + minRandTime) / 2 {
	//	maxRandTime = randTime
	//} else {
	//	minRandTime = randTime
	//}
	fmt.Println(rf.PrefixPrint(), "running as Candidate electing in", randTime, "ms")
	//fmt.Printf("Peer %v running as Candidate electing", rf.me)
	time.Sleep(time.Duration(randTime) * time.Millisecond)
	if rf.myState != CandidateState {
		return
	}

	rf.currentTerm++
	//rf.votedFor = rf.me

	fmt.Println(rf.PrefixPrint(), "issuing election")

	//rf.mu.Lock()

	args := &RequestVoteArgs{
		Term:         rf.currentTerm,
		CandidateId:  rf.me,
		LastLogIndex: rf.commitIndex,
		LastLogTerm:  0,
	}

	//rf.mu.Unlock()

	replyArray := make([]RequestVoteReply, len(rf.peers))
	requestInfo := rf.newAsyncRpcInfo(len(rf.peers) - 1)
	for peerIndex, _ := range rf.peers {
		if peerIndex == rf.me {
			// does not send to myself
			continue
		}
		if rf.myState != CandidateState {
			return
		}
		fmt.Println(rf.PrefixPrint(), "requesting vote from peer", peerIndex)
		rf.sendRequestVoteAsync(peerIndex, args, &replyArray[peerIndex], requestInfo)
		//rf.callAsync((*Raft).requestVoteCallBack, peerIndex, "Raft.RequestVote", args, &replyArray[peerIndex], requestInfo)
		/*
			if rf.sendRequestVote(peerIndex, args, &replyArray[peerIndex]) {
				aliveCount++
				if rf.tryFollowNewerTerm(peerIndex, replyArray[peerIndex].Term) {
					return
				}
				if replyArray[peerIndex].GrantVote {
					fmt.Println(rf.PrefixPrint(), "granted a vote by peer", peerIndex)
					voteCount++
				}
			} else {
				fmt.Println(rf.PrefixPrint(), "cannot reach peer", peerIndex, "when requesting a vote")
			}
		*/
	}
	fmt.Println(rf.PrefixPrint(), "all vote requests sent, waiting for replies")
	//rf.requestVoteChan <- requestInfo
	requestInfo.Wait()

}

func (rf *Raft) requestVoteWorker() {
	fmt.Println(rf.PrefixPrint(), "RequestVote rpc info worker initiated")
	for {
		info := <-rf.requestVoteChan
		fmt.Println(rf.PrefixPrint(), "worker checking on a collected vote info")
		info.Cond.L.Lock()
		if info.MustExit {
			fmt.Println(rf.PrefixPrint(), "sees an abandoned RequestVote sending action")
			info.Cond.L.Unlock()
			info.Cond.Broadcast()
			continue
		}
		if rf.myState == LeaderState {
			// already elected leader
			// ignore this info
			fmt.Println(rf.PrefixPrint(), "already in Leader state, ignore all votes")
			info.MustExit = true
			info.Cond.L.Unlock()
			info.Cond.Broadcast()
			continue
		}
		fmt.Println(rf.PrefixPrint(), "got", info.SuccessCount, "votes in", info.AliveCount, "alive peers", info.TotalCount, "total peers")
		if info.SuccessCount+1 > info.TotalCount/2 {
			// sufficient for an leader election
			// no need to wait longer
			fmt.Println(rf.PrefixPrint(), "elected leader at term", rf.currentTerm)
			rf.myState = LeaderState
			rf.votedFor = rf.me
			info.MustExit = true
			info.Cond.L.Unlock()
			info.Cond.Broadcast()
			continue
		} else {
			// not all info collected
			// must put it back
			fmt.Println(rf.PrefixPrint(), "vote info not sufficient for a decision")
			rf.requestVoteChan <- info
		}
		info.Cond.L.Unlock()
	}
}

func (rf *Raft) PeriodCheckElect() {
	// check clear flag every fixed period of time, avioding using go timer
	// restart this timer if clear flag is set
	checkCount := 200
	divDuration := rf.electWaitDuration / checkCount
	for {
		//rf.mu.Lock()
		if rf.myState != FollowerState {
			// can only return from here
			//rf.mu.Unlock()
			return
		}
		for checkIndex := 0; checkIndex < checkCount; checkIndex++ {
			if rf.timerCleared {
				// restart timer
				break
			}
			if rf.votedFor == -1 {
				fmt.Println(rf.PrefixPrint(), "has no leader, converting to a candidate")
				rf.myState = CandidateState
				return
			}
			//rf.mu.Unlock()
			time.Sleep(time.Millisecond * time.Duration(divDuration))
			//rf.mu.Lock()
			if rf.myState != FollowerState {
				return
			}
		}
		if !rf.timerCleared {
			// not brought here by a timeout event
			// timer manually reset
			fmt.Println(rf.PrefixPrint(), "did not receive heartbeat signal from supposed leader peer", rf.votedFor, "in", rf.electWaitDuration, "ms, converting to a candidate")
			// no leader now
			rf.votedFor = -1
			rf.myState = CandidateState
			return
		} else {
			// clear clear flag
			rf.timerCleared = false
			// ticker continue
		}
		//rf.mu.Unlock()
	}
}

/*
func (rf *Raft) runLeader() {
	//rf.mu.Lock()
	args := &AppendEntriesArgs{
		Term:     rf.currentTerm,
		LeaderId: rf.me,
	}
	//rf.mu.Unlock()
	replyArray := make([]AppendEntriesReply, len(rf.peers))
	for {
		// send heart beat signal
		aliveCount := 0
		successCount := 0
		for peerIndex, _ := range rf.peers {
			if peerIndex == rf.me {
				continue
			}
			fmt.Println(rf.PrefixPrint(), "sending heartbeat signal to peer", peerIndex)
			if rf.sendAppendEntries(peerIndex, args, &replyArray[peerIndex]) {
				aliveCount++
				if rf.tryFollowNewerTerm(peerIndex, replyArray[peerIndex].Term) {
					return
				}
				fmt.Println(rf.PrefixPrint(), "receive heartbeat response from peer", peerIndex, "success", replyArray[peerIndex].Success)
				if replyArray[peerIndex].Success {
					successCount++
				}
			} else {
				fmt.Println(rf.PrefixPrint(), "found peer", peerIndex, "unreachable when sending heartbeats")
			}
		}
		// account for itself
		if successCount <= aliveCount / 2 {
			// too few peers alive
			// retreat as candidate
			//rf.mu.Lock()
			fmt.Println(rf.PrefixPrint(), "found", successCount, "heartbeat success peers too few for", aliveCount, "alive peers, retreating to a candidate")
			rf.myState = CandidateState
			//rf.mu.Unlock()
			return
		}
		fmt.Println(rf.PrefixPrint(), "got", successCount, "heartbeat successes in", aliveCount, "alive peers")
		time.Sleep(time.Millisecond * time.Duration(rf.heartBeatWaitDuration))
	}
}
*/

func (rf *Raft) appendEntriesCallBack(ok bool, peerIndex int, args *AppendEntriesArgs, reply *AppendEntriesReply, info *AsyncRpcInfo) {
	if ok {
		info.IncrementAliveCount()
		if rf.tryFollowNewerTerm(peerIndex, reply.Term) {
			info.SetMustExit()
		} else {
			if reply.Success {
				info.IncrementSuccessCount()
			}
		}
	} else {
		fmt.Println(rf.PrefixPrint(), "found peer", peerIndex, "unreachable when sending heartbeats")
	}
	info.IncrementCurrentCount()
	if info.SuccessCount+info.TotalCount-info.CurrentCount < info.TotalCount/2 {
		// must retreat to candidate
		fmt.Println(rf.PrefixPrint(), "found", info.SuccessCount, "heartbeat success peers too few for", info.AliveCount, "alive peers,", info.CurrentCount, "current responses", ", retreating to a candidate")
		rf.myState = CandidateState
		info.SetMustExit()
	}
}

func (rf *Raft) sendAppendEntriesAsync(peerIndex int, args *AppendEntriesArgs, reply *AppendEntriesReply, info *AsyncRpcInfo) {
	go func() {
		if info.MustExit {
			return
		}
		sendBegin := time.Now()
		ok := rf.sendAppendEntries(peerIndex, args, reply)
		sendEnd := time.Now()
		fmt.Println(rf.PrefixPrint(), "heartbeat response from peer", peerIndex, "received in", sendEnd.Sub(sendBegin), "success", reply.Success)
		if info.MustExit {
			return
		}
		rf.appendEntriesCallBack(ok, peerIndex, args, reply, info)
	}()
}

func (rf *Raft) runLeader() {
	//rf.mu.Lock()
	args := &AppendEntriesArgs{
		Term:     rf.currentTerm,
		LeaderId: rf.me,
	}
	//rf.mu.Unlock()
	replyArray := make([]AppendEntriesReply, len(rf.peers))
	info := rf.newAsyncRpcInfo(len(rf.peers))
	for {
		if rf.myState != LeaderState {
			return
		}
		// send heart beat signal
		for peerIndex, _ := range rf.peers {
			if peerIndex == rf.me {
				continue
			}
			fmt.Println(rf.PrefixPrint(), "sending heartbeat signal to peer", peerIndex)
			rf.sendAppendEntriesAsync(peerIndex, args, &replyArray[peerIndex], info)
			/*
				if rf.sendAppendEntries(peerIndex, args, &replyArray[peerIndex]) {
					aliveCount++
					if rf.tryFollowNewerTerm(peerIndex, replyArray[peerIndex].Term) {
						return
					}
					fmt.Println(rf.PrefixPrint(), "receive heartbeat response from peer", peerIndex, "success", replyArray[peerIndex].Success)
					if replyArray[peerIndex].Success {
						successCount++
					}
				} else {
					fmt.Println(rf.PrefixPrint(), "found peer", peerIndex, "unreachable when sending heartbeats")
				}
			*/
		}
		fmt.Println(rf.PrefixPrint(), "all heartbeats sent, sleeping for", rf.heartBeatWaitDuration, "then send heartbeats again")
		//rf.appendEntriesChan <- info
		/*
			info.Wait()
			// account for itself
			if info.SuccessCount <= info.AliveCount / 2 {
				// too few peers alive
				// retreat as candidate
				//rf.mu.Lock()
				fmt.Println(rf.PrefixPrint(), "found", info.SuccessCount, "heartbeat success peers too few for", info.AliveCount, "alive peers, retreating to a candidate")
				rf.myState = CandidateState
				//rf.mu.Unlock()
				return
			}
			fmt.Println(rf.PrefixPrint(), "got", info.SuccessCount, "heartbeat successes in", info.AliveCount, "alive peers")
		*/
		time.Sleep(time.Millisecond * time.Duration(rf.heartBeatWaitDuration))
	}
}

func (rf *Raft) appendEntriesWorker() {
	fmt.Println(rf.PrefixPrint(), "AppendEntries rpc info worker initiated")
	for {
		info := <-rf.appendEntriesChan
		info.Cond.L.Lock()
		if info.MustExit {
			fmt.Println(rf.PrefixPrint(), "sees an abandoned AppendEntry sending action")
			info.Cond.L.Unlock()
			continue
		}
		if info.CurrentCount >= info.TotalCount {
			fmt.Println(rf.PrefixPrint(), "all info on an AppendEntry rpc collected, processing by worker")
			// all info collected
			if info.SuccessCount <= info.AliveCount/2 {
				// too few peers alive
				// retreat as candidate
				fmt.Println(rf.PrefixPrint(), "found", info.SuccessCount, "heartbeat success peers too few for", info.AliveCount, "alive peers, retreating to a candidate")
				rf.myState = CandidateState
				info.Cond.L.Unlock()
				continue
			}
			fmt.Println(rf.PrefixPrint(), "got", info.SuccessCount, "heartbeat successes in", info.AliveCount, "alive peers")
		} else {
			// not all info collected
			// must put it back
			rf.appendEntriesChan <- info
		}
		info.Cond.L.Unlock()
	}
}

func (rf *Raft) Run() {
	//go rf.appendEntriesWorker()
	//go rf.requestVoteWorker()
	for {
		//rf.mu.Lock()
		fmt.Println(rf.PrefixPrint(), "taking a roll")
		switch rf.myState {
		case LeaderState:
			for {
				if rf.myState != LeaderState {
					fmt.Println(rf.PrefixPrint(), "exiting leader state")
					//rf.mu.Unlock()
					break
				}
				//rf.mu.Unlock()
				rf.runLeader()
			}
			break
		case FollowerState:
			rf.PeriodCheckElect()
			break
		case CandidateState:
			rf.runElect()
			break
		default:
			panic("Invalid peer state!")
		}
	}
}

//
// the service or tester wants to create a Raft server. the ports
// of all the Raft servers (including this one) are in peers[]. this
// server's port is peers[me]. all the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects Raft to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
//
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{
		appendEntriesChan: make(chan *AsyncRpcInfo),
		requestVoteChan:   make(chan *AsyncRpcInfo),
	}
	rf.peers = peers
	rf.persister = persister
	rf.me = me

	// Your initialization code here (2A, 2B, 2C).

	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())

	rf.votedFor = -1
	rf.electWaitDuration = 300
	rf.heartBeatWaitDuration = 120
	rf.timerCleared = false

	rf.followerCond = sync.NewCond(&sync.Mutex{})
	rf.leaderCond = sync.NewCond(&sync.Mutex{})
	rf.candidateCond = sync.NewCond(&sync.Mutex{})
	rf.myState = FollowerState

	go rf.Run()

	return rf
}
