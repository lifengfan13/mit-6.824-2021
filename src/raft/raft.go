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
	//	"bytes"

	"bytes"
	"log"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	//	"6.824/labgob"
	"6.824/labgob"
	"6.824/labrpc"
)

//
// as each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make(). set
// CommandValid to true to indicate that the ApplyMsg contains a newly
// committed log entry.
//
// in part 2D you'll want to send other kinds of messages (e.g.,
// snapshots) on the applyCh, but set CommandValid to false for these
// other uses.
//

type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int
	CommandTerm  int

	// For 2D:
	SnapshotValid bool
	Snapshot      []byte
	SnapshotTerm  int
	SnapshotIndex int
}

//node state
type NodeState int

const (
	Follower   NodeState = 0
	Candidater NodeState = 1
	Leader     NodeState = 2
)

//
// A Go object implementing a single Raft peer.
//
type Raft struct {
	mu        sync.Mutex          // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	dead      int32               // set by Kill()

	// Your data here (2A, 2B, 2C).
	// Look at the paper's Figure 2 for a description of what
	// state a Raft server must maintain.

	applyCond sync.Cond
	applyCh   chan ApplyMsg
	state     NodeState

	currentTerm int
	votedFor    int
	log         Log

	commitIndex int
	lastApplied int

	nextIndex  []int
	matchIndex []int

	electionTimer *time.Timer
	heartTimer    *time.Timer
}

// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	// Your code here (2A).
	return rf.currentTerm, rf.state == Leader
}

//
// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
//
func (rf *Raft) persist() {
	// Your code here (2C).
	// Example:
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	e.Encode(rf.currentTerm)
	e.Encode(rf.votedFor)
	e.Encode(rf.log)
	data := w.Bytes()
	rf.persister.SaveRaftState(data)
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
	r := bytes.NewBuffer(data)
	d := labgob.NewDecoder(r)
	var term int
	var voteFor int
	var log Log
	if d.Decode(&term) != nil ||
		d.Decode(&voteFor) != nil ||
		d.Decode(&log) != nil {
		DPrintf("%v restores persisted state failed", rf.me)
	} else {
		rf.currentTerm = term
		rf.votedFor = voteFor
		rf.log = log
	}
}

//
// A service wants to switch to snapshot.  Only do so if Raft hasn't
// have more recent info since it communicate the snapshot on applyCh.
//
func (rf *Raft) CondInstallSnapshot(lastIncludedTerm int, lastIncludedIndex int, snapshot []byte) bool {

	// Your code here (2D).

	return true
}

// the service says it has created a snapshot that has
// all info up to and including index. this means the
// service no longer needs the log through (and including)
// that index. Raft should now trim its log as much as possible.
func (rf *Raft) Snapshot(index int, snapshot []byte) {
	// Your code here (2D).

}

//
// example RequestVote RPC handler.
//
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (2A, 2B).
	rf.mu.Lock()
	defer rf.mu.Unlock()
	defer rf.persist()
	// Your code here (2A, 2B).
	if rf.currentTerm > args.Term || (rf.currentTerm == args.Term && rf.votedFor != -1 && rf.votedFor != args.CandidateId) {
		reply.Term, reply.VoteGranted = rf.currentTerm, false
		return
	}

	if rf.currentTerm < args.Term {
		reply.VoteGranted = true
		rf.state, rf.currentTerm, rf.votedFor = Follower, args.Term, -1
	}

	//比较日志那个新
	lastIndex := rf.log.lastIndex()
	lastTerm := rf.log.entry(lastIndex).Term
	isUptoDate := lastTerm < args.LastLogTerm || (lastTerm == args.LastLogTerm && lastIndex <= args.LastLogIndex)

	DPrintf("%v: T %v voteFor %v\n", rf.me, rf.currentTerm, rf.votedFor)
	if !isUptoDate {
		reply.Term, reply.VoteGranted = rf.currentTerm, false
		return
	}

	rf.votedFor = args.CandidateId
	rf.resetElectionTimeout()
	reply.Term, reply.VoteGranted = rf.currentTerm, true
}

func (rf *Raft) RequestAppendEntries(args *AppendEntriesAags, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	defer rf.persist()
	//rule1
	if rf.currentTerm > args.Term {
		reply.Success = false
		reply.Term = rf.currentTerm
		return
	}

	if rf.currentTerm < args.Term {
		rf.currentTerm, rf.votedFor = args.Term, -1
	}

	rf.state = Follower
	rf.resetElectionTimeout()

	//rule2
	if args.PrevLogIndex < rf.log.firstIndex() {
		reply.Success = false
		reply.Term = 0
		return
	}

	//rule3
	if rf.log.lastIndex() < args.PrevLogIndex {
		reply.Success = false
		reply.Term = rf.currentTerm
		reply.ConflictVaild = true
		reply.ConflictIndex = rf.log.lastIndex() + 1
		reply.ConflictTerm = -1
		return
	}

	//rule4
	if rf.log.entry(args.PrevLogIndex).Term != args.PrevLogTerm {
		reply.Success = false
		reply.Term = rf.currentTerm
		reply.ConflictVaild = true

		firstIndex := rf.log.firstIndex()
		conflictTerm := rf.log.entry(args.PrevLogIndex).Term
		index := args.PrevLogIndex - 1

		for index >= firstIndex && rf.log.entry(index).Term == conflictTerm {
			index--
		}

		reply.ConflictIndex = index + 1
		reply.ConflictTerm = conflictTerm
		return
	}

	rf.log.AppendLogs(args.PrevLogIndex, args.Entries)

	if rf.commitIndex < args.LeaderCommit {
		rf.commitIndex = args.LeaderCommit
		newCommit := args.PrevLogIndex + len(args.Entries)
		if rf.commitIndex > newCommit {
			rf.commitIndex = newCommit
		}
		DPrintf("%v: commit %v \n", rf.me, rf.commitIndex)
		rf.applyCond.Signal()
	}

	reply.Success = true
	reply.Term = rf.currentTerm
}

func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}

func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesAags, reply *AppendEntriesReply) bool {
	ok := rf.peers[server].Call("Raft.RequestAppendEntries", args, reply)
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
// term. the third return value is true if this server believes it is
// the leader.
//
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.state != Leader {
		return -1, rf.currentTerm, false
	}

	e := Entry{rf.currentTerm, command}
	DPrintf("%v: receive a log %+v in Term %v\n", rf.me, e, rf.currentTerm)
	rf.log.append(e)
	rf.persist()
	rf.startAppendEntrys(false)

	// Your code here (2B).

	return rf.log.lastIndex(), rf.currentTerm, true
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
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

func (rf *Raft) advanceCommit() {
	if rf.state != Leader {
		log.Fatalf("advanceCommit state: %v\n", rf.state)
	}

	start := rf.commitIndex + 1
	if start < rf.log.firstIndex() {
		start = rf.log.firstIndex()
	}

	for index := start; index <= rf.log.lastIndex(); index++ {
		if rf.log.entry(index).Term != rf.currentTerm {
			continue
		}

		n := 1
		for i, _ := range rf.matchIndex {
			if i != rf.me && rf.matchIndex[i] >= index {
				n++
			}
		}

		if n > len(rf.peers)/2 {
			DPrintf("%v: Commit %v \n", rf.me, index)
			rf.commitIndex = index
		}
	}
	rf.applyCond.Signal()
}

func (rf *Raft) handleAppendEntries(peer int, args *AppendEntriesAags, reply *AppendEntriesReply) {
	DPrintf("%v AppendEntries reply from %v:%v\n", rf.me, peer, reply)
	if rf.state == Leader && rf.currentTerm == args.Term {
		if reply.Success {
			newNextIndex := args.PrevLogIndex + len(args.Entries) + 1
			newMatchIndex := newNextIndex - 1

			if newNextIndex > rf.nextIndex[peer] {
				rf.nextIndex[peer] = newNextIndex
			}

			if newMatchIndex > rf.matchIndex[peer] {
				rf.matchIndex[peer] = newMatchIndex
			}
			rf.advanceCommit()
			DPrintf("%v: handleAppendEntries success: peer %v nextIndex %v matchIndex %v\n", rf.me, peer, rf.nextIndex[peer], rf.matchIndex[peer])
		} else {
			if rf.currentTerm < reply.Term {
				rf.currentTerm, rf.state, rf.votedFor = reply.Term, Follower, -1
				rf.resetElectionTimeout()
				rf.persist()
			} else if rf.currentTerm == reply.Term {
				if reply.ConflictVaild {
					if reply.ConflictIndex > rf.log.lastIndex() {
						rf.nextIndex[peer] = rf.log.lastIndex()
					} else if reply.ConflictIndex < rf.log.firstIndex() {
						rf.nextIndex[peer] = rf.log.firstIndex() + 1
					} else {
						if rf.log.entry(reply.ConflictIndex).Term == reply.ConflictTerm {
							lastIndex := rf.log.lastIndex()
							index := reply.ConflictIndex
							for index <= lastIndex && rf.log.entry(index).Term == reply.ConflictTerm {
								index++
							}
							rf.nextIndex[peer] = index
						} else {
							rf.nextIndex[peer] = reply.ConflictIndex
						}
					}
				} else if rf.nextIndex[peer] > 1 {
					rf.nextIndex[peer] -= 1
				}
			}
		}
	}
}

func (rf *Raft) requestAppendEntries(peer int, heartBeat bool) {
	rf.mu.Lock()
	nextIndex := rf.nextIndex[peer]

	if nextIndex <= rf.log.firstIndex() {
		nextIndex = rf.log.firstIndex() + 1
	}

	if nextIndex-1 > rf.log.lastIndex() {
		nextIndex = rf.log.lastIndex() + 1
	}

	args := &AppendEntriesAags{rf.currentTerm, rf.me, nextIndex - 1, rf.log.entry(nextIndex - 1).Term,
		make([]Entry, rf.log.lastIndex()-nextIndex+1), rf.commitIndex}

	copy(args.Entries, rf.log.nextSlice(nextIndex))
	rf.mu.Unlock()

	DPrintf("%v:requestAppendEntries to %v: %v\n", rf.me, peer, args)

	reply := &AppendEntriesReply{}
	if rf.sendAppendEntries(peer, args, reply) {
		rf.mu.Lock()
		defer rf.mu.Unlock()
		rf.handleAppendEntries(peer, args, reply)
	}
}

func (rf *Raft) startAppendEntrys(heartBeat bool) {
	if heartBeat {
		DPrintf("%v:start heartBeat T %v\n", rf.me, rf.currentTerm)
	}
	for i := range rf.peers {
		if i != rf.me {
			if rf.log.lastIndex() > rf.nextIndex[i] || heartBeat {
				go rf.requestAppendEntries(i, heartBeat)
			}
		}
	}
}

func (rf *Raft) requestVote(peer int, args *RequestVoteArgs, vote *int) {
	reply := &RequestVoteReply{}

	DPrintf("%v:sendRequestVote to %v: %v\n", rf.me, peer, args)
	if rf.sendRequestVote(peer, args, reply) {
		rf.mu.Lock()
		defer rf.mu.Unlock()

		DPrintf("%v RequestVote reply from %v:%v\n", rf.me, peer, reply)
		if rf.currentTerm == args.Term && rf.state == Candidater {
			if reply.VoteGranted {
				*vote++
				if *vote > len(rf.peers)/2 {
					DPrintf("%v: become Leader in Term %v LastIndex %v\n", rf.me, rf.currentTerm, rf.log.lastIndex())
					rf.state = Leader
					rf.startAppendEntrys(true)
					rf.resetHeartTimeout()

					for i := range rf.nextIndex {
						rf.nextIndex[i] = rf.log.lastIndex() + 1
					}
				}
			} else if rf.currentTerm < reply.Term {
				rf.currentTerm, rf.state, rf.votedFor = reply.Term, Follower, -1
				rf.resetElectionTimeout()
				rf.persist()
			}
		}
	}
}

//send vote to all
func (rf *Raft) StartSelection() {
	DPrintf("%v: tick T %v\n", rf.me, rf.currentTerm)
	vote := 1
	args := RequestVoteArgs{rf.currentTerm, rf.me, rf.log.lastIndex(), rf.log.entry(rf.log.lastIndex()).Term}
	for i := range rf.peers {
		if rf.me != i {
			go rf.requestVote(i, &args, &vote)
		}
	}
}

// The ticker go routine starts a new election if this peer hasn't received
// heartsbeats recently.
func (rf *Raft) ticker() {
	for rf.killed() == false {
		// Your code here to check if a leader election should
		// be started and to randomize sleeping time using
		// time.Sleep().
		select {
		case <-rf.heartTimer.C:
			rf.mu.Lock()
			if rf.state == Leader {
				rf.startAppendEntrys(true) //heartBeat
				rf.resetHeartTimeout()
			}
			rf.mu.Unlock()
		case <-rf.electionTimer.C:
			rf.mu.Lock()
			if rf.state != Leader {
				rf.currentTerm += 1
				rf.state = Candidater
				rf.votedFor = rf.me
				rf.persist()
				rf.StartSelection()
				rf.resetElectionTimeout()
			}
			rf.mu.Unlock()
		}
	}
}

func (rf *Raft) applier() {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	for rf.killed() == false {
		if rf.lastApplied < rf.commitIndex && rf.lastApplied < rf.log.lastIndex() && rf.lastApplied+1 > rf.log.firstIndex() {
			rf.lastApplied++
			reply := ApplyMsg{
				CommandValid: true,
				CommandIndex: rf.lastApplied,
				Command:      rf.log.entry(rf.lastApplied).Command,
			}

			DPrintf("%v: applier index: %v\n", rf.me, reply.CommandIndex)

			rf.mu.Unlock()
			rf.applyCh <- reply
			rf.mu.Lock()
		} else {
			rf.applyCond.Wait()
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
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me
	rf.dead = 0

	// Your initialization code here (2A, 2B, 2C).
	rf.applyCh = applyCh
	rf.state = Follower

	rf.currentTerm = 0
	rf.applyCond = *sync.NewCond(&rf.mu)

	rf.votedFor = -1
	rf.log = makeEmptyLog()

	rf.nextIndex = make([]int, len(peers))
	rf.matchIndex = make([]int, len(peers))

	rf.electionTimer = time.NewTimer(0)
	rf.heartTimer = time.NewTimer(0)
	rf.resetElectionTimeout()
	rf.resetHeartTimeout()

	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())

	// start ticker goroutine to start elections
	go rf.ticker()

	go rf.applier()

	return rf
}

func (rf *Raft) resetElectionTimeout() {
	i := rand.Int31n(300)
	t := time.Millisecond * time.Duration(i+200)
	rf.electionTimer.Reset(t)
}

func (rf *Raft) resetHeartTimeout() {
	i := rand.Int31n(50)
	t := time.Millisecond * time.Duration(i+100)
	rf.heartTimer.Reset(t)
}
