package gamestats

import ujson.*

class PatternsSuite extends munit.FunSuite:
  private def event(
      id: String,
      eventType: String,
      playerId: Option[String],
      data: Value = Obj(),
      second: Int = 0
  ): Event =
    val timestamp = java.time.Instant.parse("2026-10-11T12:00:00Z").plusSeconds(second).toString
    Event(id, timestamp, "test-game", "1.0", "pattern-match", playerId, eventType, None, data)

  private def combat(events: List[Event]): Batch =
    Batch("pattern-match", "combat-arena", "COMPLETE", events)

  private def racing(status: String, events: List[Event]): Batch =
    Batch("pattern-match", "racing-circuit", status, events)

  test("KILL_STREAK detects three eliminations within ten seconds") {
    val batch = combat(List(
      event("k1", "PLAYER_ELIMINATED", Some("p1"), Obj("targetId" -> Str("a")), 0),
      event("k2", "PLAYER_ELIMINATED", Some("p1"), Obj("targetId" -> Str("b")), 4),
      event("k3", "PLAYER_ELIMINATED", Some("p1"), Obj("targetId" -> Str("c")), 8)
    ))

    val pattern = PatternDetectors.detect(batch).head
    assertEquals(pattern.patternType, "KILL_STREAK")
    assertEquals(pattern.playerId, "p1")
    assertEquals(pattern.detail("count").num, 3.0)
    assertEquals(pattern.detail("start").str, batch.events.head.timestamp)
    assertEquals(pattern.detail("end").str, batch.events.last.timestamp)
  }

  test("KILL_STREAK rejects three eliminations spread across eleven seconds") {
    val batch = combat(List(
      event("k1", "PLAYER_ELIMINATED", Some("p1"), Obj("targetId" -> Str("a")), 0),
      event("k2", "PLAYER_ELIMINATED", Some("p1"), Obj("targetId" -> Str("b")), 5),
      event("k3", "PLAYER_ELIMINATED", Some("p1"), Obj("targetId" -> Str("c")), 11)
    ))

    assertEquals(PatternDetectors.detect(batch).filter(_.patternType == "KILL_STREAK"), Nil)
  }

  test("SUSPICIOUS_ABANDON detects a losing player who stays disconnected") {
    val batch = combat(List(
      event("lead1", "PLAYER_ELIMINATED", Some("p2"), Obj("targetId" -> Str("x")), 1),
      event("lead2", "PLAYER_ELIMINATED", Some("p2"), Obj("targetId" -> Str("y")), 2),
      event("disconnect", "PLAYER_DISCONNECTED", Some("p1"), second = 10),
      event("later", "PLAYER_JOINED", Some("p3"), second = 71)
    ))

    val pattern = PatternDetectors.detect(batch).find(_.patternType == "SUSPICIOUS_ABANDON").get
    assertEquals(pattern.playerId, "p1")
    assertEquals(pattern.detail("kills").num, 0.0)
    assertEquals(pattern.detail("leaderKills").num, 2.0)
  }

  test("SUSPICIOUS_ABANDON rejects a reconnection within sixty seconds") {
    val batch = combat(List(
      event("lead", "PLAYER_ELIMINATED", Some("p2"), Obj("targetId" -> Str("x")), 1),
      event("disconnect", "PLAYER_DISCONNECTED", Some("p1"), second = 10),
      event("reconnect", "PLAYER_RECONNECTED", Some("p1"), second = 69)
    ))

    assertEquals(PatternDetectors.detect(batch).filter(_.patternType == "SUSPICIOUS_ABANDON"), Nil)
  }

  test("SUSPICIOUS_ABANDON waits until sixty seconds have been observed") {
    val batch = combat(List(
      event("lead", "PLAYER_ELIMINATED", Some("p2"), Obj("targetId" -> Str("x")), 1),
      event("disconnect", "PLAYER_DISCONNECTED", Some("p1"), second = 10),
      event("last-event", "PLAYER_JOINED", Some("p3"), second = 50)
    ))

    assertEquals(PatternDetectors.detect(batch).filter(_.patternType == "SUSPICIOUS_ABANDON"), Nil)
  }

  test("COMEBACK detects the winner starting in the back half") {
    val batch = racing("COMPLETE", List(
      event("start", "MATCH_STARTED", None, Obj("laps" -> Num(4))),
      event("mid1", "LAP_COMPLETED", Some("p1"), Obj("lap" -> Num(2), "position" -> Num(4))),
      event("mid2", "LAP_COMPLETED", Some("p2"), Obj("lap" -> Num(2), "position" -> Num(1))),
      event("mid3", "LAP_COMPLETED", Some("p3"), Obj("lap" -> Num(2), "position" -> Num(2))),
      event("mid4", "LAP_COMPLETED", Some("p4"), Obj("lap" -> Num(2), "position" -> Num(3))),
      event("finish", "MATCH_FINISHED", None, Obj("classification" -> Arr(Str("p1"), Str("p2"), Str("p3"), Str("p4"))))
    ))

    val pattern = PatternDetectors.detect(batch).head
    assertEquals(pattern.patternType, "COMEBACK")
    assertEquals(pattern.playerId, "p1")
    assertEquals(pattern.detail("lap").num, 2.0)
    assertEquals(pattern.detail("position").num, 4.0)
  }

  test("COMEBACK is not emitted for an incomplete match") {
    val batch = racing("TIMEOUT", List(
      event("start", "MATCH_STARTED", None, Obj("laps" -> Num(4))),
      event("mid1", "LAP_COMPLETED", Some("p1"), Obj("lap" -> Num(2), "position" -> Num(4))),
      event("mid2", "LAP_COMPLETED", Some("p2"), Obj("lap" -> Num(2), "position" -> Num(1))),
      event("finish", "MATCH_FINISHED", None, Obj("classification" -> Arr(Str("p1"), Str("p2"))))
    ))

    assertEquals(PatternDetectors.detect(batch).filter(_.patternType == "COMEBACK"), Nil)
  }

  test("service stores and filters patterns with each match") {
    val service = new AnalysisService()
    val batch = combat(List(
      event("k1", "PLAYER_ELIMINATED", Some("p1"), Obj("targetId" -> Str("a")), 0),
      event("k2", "PLAYER_ELIMINATED", Some("p1"), Obj("targetId" -> Str("b")), 4),
      event("k3", "PLAYER_ELIMINATED", Some("p1"), Obj("targetId" -> Str("c")), 8)
    ))
    service.analyze(batch)

    assertEquals(service.getPatterns(Some("combat-arena"), Some("KILL_STREAK"), Some("pattern-match")).size, 1)
    assertEquals(service.getPatterns(Some("racing-circuit")).size, 0)
  }

  test("player stats accumulate across matches and combat leaderboard ranks kills then K/D") {
    val service = new AnalysisService()
    val first = combat(List(
      event("kill1", "PLAYER_ELIMINATED", Some("p1"), Obj("targetId" -> Str("x"))),
      event("fire1", "SHOT_FIRED", Some("p1"), Obj("weapon" -> Str("rifle"))),
      event("hit1", "SHOT_HIT", Some("p1"), Obj("targetId" -> Str("x"), "damage" -> Num(30), "headshot" -> Bool(true)))
    )).copy(matchId = "combat-1")
    val second = first.copy(matchId = "combat-2", events = List(
      event("kill2", "PLAYER_ELIMINATED", Some("p1"), Obj("targetId" -> Str("y"))),
      event("kill3", "PLAYER_ELIMINATED", Some("p2"), Obj("targetId" -> Str("z"))),
      event("death", "PLAYER_ELIMINATED", Some("p2"), Obj("targetId" -> Str("p1")))
    ))
    service.analyze(first)
    service.analyze(second)

    val stats = service.getPlayerStats("p1").get
    val combatStats = stats("games")("combat-arena")
    assertEquals(combatStats("matches").num, 2.0)
    assertEquals(combatStats("kills").num, 2.0)
    assertEquals(combatStats("deaths").num, 1.0)
    assertEquals(combatStats("damage").num, 30.0)
    assertEquals(combatStats("headshotPct").num, 100.0)
    assertEquals(service.leaderboard("combat-arena").head("playerId").str, "p1")
  }

  test("racing leaderboard orders wins before best lap") {
    val service = new AnalysisService()
    val firstRace = racing("COMPLETE", List(
      event("start1", "MATCH_STARTED", None, Obj("laps" -> Num(2))),
      event("p1lap1", "LAP_COMPLETED", Some("p1"), Obj("lap" -> Num(1), "lapTimeMs" -> Num(90000), "position" -> Num(2))),
      event("p2lap1", "LAP_COMPLETED", Some("p2"), Obj("lap" -> Num(1), "lapTimeMs" -> Num(80000), "position" -> Num(1))),
      event("finish1", "MATCH_FINISHED", None, Obj("classification" -> Arr(Str("p1"), Str("p2"))))
    )).copy(matchId = "race-1")
    val secondRace = firstRace.copy(matchId = "race-2", events = List(
      event("start2", "MATCH_STARTED", None, Obj("laps" -> Num(2))),
      event("p1lap2", "LAP_COMPLETED", Some("p1"), Obj("lap" -> Num(1), "lapTimeMs" -> Num(85000), "position" -> Num(2))),
      event("p2lap2", "LAP_COMPLETED", Some("p2"), Obj("lap" -> Num(1), "lapTimeMs" -> Num(70000), "position" -> Num(1))),
      event("finish2", "MATCH_FINISHED", None, Obj("classification" -> Arr(Str("p2"), Str("p1"))))
    ))
    service.analyze(firstRace)
    service.analyze(secondRace)

    val leaderboard = service.leaderboard("racing-circuit")
    assertEquals(leaderboard.head("playerId").str, "p2")
    assertEquals(service.getPlayerStats("p1").get("games")("racing-circuit")("wins").num, 1.0)
  }