package gamestats

import ujson.*

class AnalyzersSuite extends munit.FunSuite:
  private def event(
      id: String,
      eventType: String,
      playerId: Option[String],
      data: Value = Obj(),
      timestamp: String = "2026-10-10T15:00:00Z"
  ): Event = Event(id, timestamp, "test-game", "1.0", "match-1", playerId, eventType, None, data)

  test("combat analyzer calculates per-player and match statistics") {
    val batch = Batch("m-1", "combat-arena", "COMPLETE", List(
      event("start", "MATCH_STARTED", None, timestamp = "2026-10-10T15:00:00Z"),
      event("fire1", "SHOT_FIRED", Some("p1")),
      event("fire2", "SHOT_FIRED", Some("p1")),
      event("hit1", "SHOT_HIT", Some("p1"), Obj("targetId" -> Str("p2"), "damage" -> Num(40), "headshot" -> Bool(true))),
      event("hit2", "SHOT_HIT", Some("p1"), Obj("targetId" -> Str("p2"), "damage" -> Num(20), "headshot" -> Bool(false))),
      event("kill", "PLAYER_ELIMINATED", Some("p1"), Obj("targetId" -> Str("p2"), "weapon" -> Str("rifle"))),
      event("finish", "MATCH_FINISHED", None, Obj("winnerId" -> Str("p1"), "scores" -> Obj()), "2026-10-10T15:00:10Z")
    ))

    val result = new CombatArenaAnalyzer().analyze(batch)
    val p1 = result.players.collectFirst { case stats: CombatPlayerStats if stats.playerId == "p1" => stats }.get
    val p2 = result.players.collectFirst { case stats: CombatPlayerStats if stats.playerId == "p2" => stats }.get
    val stats = result.stats.asInstanceOf[CombatMatchStats]
    assertEquals(p1.kills, 1)
    assertEquals(p1.deaths, 0)
    assertEquals(p1.kd, 1.0)
    assertEquals(p1.avgDamage, 30.0)
    assertEquals(p1.headshotPct, 50.0)
    assertEquals(p1.shotsFired, 2)
    assertEquals(p1.shotsHit, 2)
    assertEquals(p1.accuracy, 1.0)
    assertEquals(p2.deaths, 1)
    assertEquals(stats.durationMs, Some(10000L))
    assertEquals(stats.totalDamage, 60.0)
    assertEquals(stats.ranking.head.playerId, "p1")
    assertEquals(stats.mvp, Some("p1"))
    assertEquals(stats.winnerId, Some("p1"))
  }

  test("racing analyzer calculates laps, position changes, penalties and classification") {
    val batch = Batch("m-2", "racing-circuit", "COMPLETE", List(
      event("lap1", "LAP_COMPLETED", Some("p1"), Obj("lap" -> Num(1), "lapTimeMs" -> Num(90000), "position" -> Num(3))),
      event("lap2", "LAP_COMPLETED", Some("p1"), Obj("lap" -> Num(2), "lapTimeMs" -> Num(80000), "position" -> Num(1))),
      event("lap3", "LAP_COMPLETED", Some("p2"), Obj("lap" -> Num(1), "lapTimeMs" -> Num(85000), "position" -> Num(2))),
      event("pos1", "POSITION_CHANGED", Some("p1"), Obj("from" -> Num(4), "to" -> Num(2))),
      event("pos2", "POSITION_CHANGED", Some("p1"), Obj("from" -> Num(2), "to" -> Num(3))),
      event("pos3", "POSITION_CHANGED", Some("p2"), Obj("from" -> Num(3), "to" -> Num(1))),
      event("pen1", "PENALTY", Some("p1"), Obj("reason" -> Str("cut"), "seconds" -> Num(5))),
      event("finish", "MATCH_FINISHED", None, Obj("classification" -> Arr(Str("p2"), Str("p1"))))
    ))

    val result = new RacingCircuitAnalyzer().analyze(batch)
    val p1 = result.players.collectFirst { case stats: RacingPlayerStats if stats.playerId == "p1" => stats }.get
    val p2 = result.players.collectFirst { case stats: RacingPlayerStats if stats.playerId == "p2" => stats }.get
    val stats = result.stats.asInstanceOf[RacingMatchStats]
    assertEquals(p1.bestLapMs, Some(80000L))
    assertEquals(p1.avgLapMs, Some(85000.0))
    assertEquals(p1.stdDevLapMs, Some(5000.0))
    assertEquals(p1.positionsGained, 1)
    assertEquals(p1.penalties, 1)
    assertEquals(p1.penaltySeconds, 5.0)
    assertEquals(stats.fastestLap, Some(FastestLap("p1", 80000L)))
    assertEquals(stats.overtakes, 2)
    assertEquals(stats.classification, Some(Arr(Str("p2"), Str("p1"))))
    assertEquals(p2.positionsGained, 2)
  }

  test("GameConsole analyzers calculate results for Connect Four, Snake and Memory") {
    val connectFour = Batch("m-c4", "connect-four", "COMPLETE", List(
      event("start-c4", "MATCH_STARTED", None, Obj("players" -> Arr(Str("p1"), Str("cpu-prolog")))),
      event("move-human", "MOVE", Some("p1")),
      event("decision", "AI_DECISION", Some("cpu-prolog"), Obj("nodes" -> Num(450), "timeMs" -> Num(8))),
      event("move-cpu", "MOVE", Some("cpu-prolog")),
      event("finish-c4", "MATCH_FINISHED", None,
        Obj("winnerId" -> Str("cpu-prolog"), "result" -> Str("LOSS"), "durationMs" -> Num(12000), "moves" -> Num(2)))
    ))
    val snake = Batch("m-snake", "snake-arena", "COMPLETE", List(
      event("start-snake", "MATCH_STARTED", None, Obj("players" -> Arr(Str("p2")))),
      event("food", "FOOD_EATEN", Some("p2"), Obj("score" -> Num(10), "length" -> Num(4))),
      event("death", "PLAYER_DIED", Some("p2"), Obj("cause" -> Str("WALL"), "score" -> Num(10))),
      event("finish-snake", "MATCH_FINISHED", None,
        Obj("winnerId" -> Null, "result" -> Str("LOSS"), "durationMs" -> Num(12000), "score" -> Num(10), "length" -> Num(4)))
    ))
    val memory = Batch("m-memory", "memory-match", "COMPLETE", List(
      event("start-memory", "MATCH_STARTED", None, Obj("players" -> Arr(Str("p3")))),
      event("flip1", "CARD_FLIPPED", Some("p3"), Obj("cardIndex" -> Num(0))),
      event("flip2", "CARD_FLIPPED", Some("p3"), Obj("cardIndex" -> Num(1))),
      event("pair", "PAIR_MATCHED", Some("p3"), Obj("pairId" -> Str("pair-1"), "attempts" -> Num(1))),
      event("finish-memory", "MATCH_FINISHED", None,
        Obj("winnerId" -> Str("p3"), "result" -> Str("WIN"), "durationMs" -> Num(25000), "pairs" -> Num(1), "attempts" -> Num(1)))
    ))

    val c4Result = GameAnalyzers.registry("connect-four").analyze(connectFour)
    val snakeResult = GameAnalyzers.registry("snake-arena").analyze(snake)
    val memoryResult = GameAnalyzers.registry("memory-match").analyze(memory)

    assertEquals(c4Result.stats.asInstanceOf[ConnectFourMatchStats].moves, 2)
    assertEquals(c4Result.stats.asInstanceOf[ConnectFourMatchStats].totalNodes, 450L)
    assertEquals(c4Result.players.find(_.playerId == "p1").get.asInstanceOf[ConnectFourPlayerStats].moves, 1)
    assertEquals(snakeResult.stats.asInstanceOf[SnakeMatchStats].score, 10)
    assertEquals(snakeResult.stats.asInstanceOf[SnakeMatchStats].deathCause, Some("WALL"))
    assertEquals(snakeResult.players.head.asInstanceOf[SnakePlayerStats].foodEaten, 1)
    assertEquals(memoryResult.stats.asInstanceOf[MemoryMatchStats].pairs, 1)
    assertEquals(memoryResult.stats.asInstanceOf[MemoryMatchStats].accuracy, 1.0)
    assertEquals(memoryResult.players.head.asInstanceOf[MemoryPlayerStats].flips, 2)
  }

  test("incomplete batches retain statistics but omit winner and classification") {
    val combat = Batch("m-3", "combat-arena", "TIMEOUT", List(
      event("kill", "PLAYER_ELIMINATED", Some("p1"), Obj("targetId" -> Str("p2"), "weapon" -> Str("rifle"))),
      event("finish", "MATCH_FINISHED", None, Obj("winnerId" -> Str("p1"), "scores" -> Obj()))
    ))
    val race = Batch("m-4", "racing-circuit", "SHUTDOWN", List(
      event("lap", "LAP_COMPLETED", Some("p1"), Obj("lap" -> Num(1), "lapTimeMs" -> Num(70000), "position" -> Num(1))),
      event("finish", "MATCH_FINISHED", None, Obj("classification" -> Arr(Str("p1"))))
    ))

    val combatResult = new CombatArenaAnalyzer().analyze(combat)
    val raceResult = new RacingCircuitAnalyzer().analyze(race)
    assert(!combatResult.complete)
    assertEquals(combatResult.stats.asInstanceOf[CombatMatchStats].winnerId, None)
    assertEquals(combatResult.stats.asInstanceOf[CombatMatchStats].ranking.head.kills, 1)
    assert(!raceResult.complete)
    assertEquals(raceResult.stats.asInstanceOf[RacingMatchStats].classification, None)
    assertEquals(raceResult.players.head.asInstanceOf[RacingPlayerStats].bestLapMs, Some(70000L))
  }

  test("analysis service stores GameConsole match results and player aggregates") {
    val service = new AnalysisService()
    val batch = Batch("m-memory-player", "memory-match", "COMPLETE", List(
      event("start", "MATCH_STARTED", None, Obj("players" -> Arr(Str("p-memory")))),
      event("flip", "CARD_FLIPPED", Some("p-memory"), Obj("cardIndex" -> Num(0))),
      event("pair", "PAIR_MATCHED", Some("p-memory"), Obj("pairId" -> Str("pair-1"), "attempts" -> Num(1))),
      event("finish", "MATCH_FINISHED", None,
        Obj("winnerId" -> Str("p-memory"), "result" -> Str("WIN"), "durationMs" -> Num(1000), "pairs" -> Num(1), "attempts" -> Num(1)))
    ))

    assert(service.analyze(batch).nonEmpty)
    assertEquals(service.getMatches("m-memory-player").head.gameId, "memory-match")
    assertEquals(service.getPlayerStats("p-memory").get("games")("memory-match")("wins").num, 1.0)
    assertEquals(service.leaderboard("memory-match").head("playerId").str, "p-memory")
  }

  test("service replaces a match and rejects unknown games") {
    val service = new AnalysisService()
    val first = Batch("m-5", "combat-arena", "TIMEOUT", Nil)
    val replacement = first.copy(status = "SHUTDOWN")
    assert(service.analyze(first).nonEmpty)
    assertEquals(service.analyze(replacement).map(_.status), Some("SHUTDOWN"))
    assertEquals(service.getMatches("m-5").map(_.status), List("SHUTDOWN"))
    assertEquals(service.analyze(first.copy(gameId = "unknown-game")), None)
  }