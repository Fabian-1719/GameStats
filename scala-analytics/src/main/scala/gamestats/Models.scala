package gamestats

import ujson.*

final case class Event(
    eventId: String,
    timestamp: String,
    gameId: String,
    gameVersion: String,
    matchId: String,
    playerId: Option[String],
    eventType: String,
    action: Option[String],
    data: Value
)

final case class Batch(matchId: String, gameId: String, status: String, events: List[Event])

sealed trait PlayerStats:
  def playerId: String
  def toJson: Obj

final case class CombatPlayerStats(
    playerId: String,
    kills: Int,
    deaths: Int,
    kd: Double,
    avgDamage: Double,
    headshotPct: Double,
    shotsFired: Int,
    shotsHit: Int,
    accuracy: Double,
    damageTotal: Double,
    headshotCount: Int
) extends PlayerStats:
  def toJson: Obj = Obj(
    "playerId" -> Str(playerId),
    "kills" -> Num(kills),
    "deaths" -> Num(deaths),
    "kd" -> Num(kd),
    "avgDamage" -> Num(avgDamage),
    "headshotPct" -> Num(headshotPct),
    "shotsFired" -> Num(shotsFired),
    "shotsHit" -> Num(shotsHit),
    "accuracy" -> Num(accuracy)
  )

final case class RacingPlayerStats(
    playerId: String,
    bestLapMs: Option[Long],
    avgLapMs: Option[Double],
    stdDevLapMs: Option[Double],
    positionsGained: Int,
    penalties: Int,
    penaltySeconds: Double,
    lapTimes: List[Long]
) extends PlayerStats:
  def toJson: Obj = Obj(
    "playerId" -> Str(playerId),
    "bestLapMs" -> bestLapMs.map(Num(_)).getOrElse(Null),
    "avgLapMs" -> avgLapMs.map(Num(_)).getOrElse(Null),
    "stdDevLapMs" -> stdDevLapMs.map(Num(_)).getOrElse(Null),
    "positionsGained" -> Num(positionsGained),
    "penalties" -> Num(penalties),
    "penaltySeconds" -> Num(penaltySeconds)
  )

final case class ConnectFourPlayerStats(
    playerId: String,
    moves: Int,
    aiDecisions: Int,
    totalNodes: Long,
    avgAiTimeMs: Option[Double]
) extends PlayerStats:
  def toJson: Obj = Obj(
    "playerId" -> Str(playerId),
    "moves" -> Num(moves),
    "aiDecisions" -> Num(aiDecisions),
    "totalNodes" -> Num(totalNodes),
    "avgAiTimeMs" -> avgAiTimeMs.map(Num(_)).getOrElse(Null)
  )

final case class SnakePlayerStats(
    playerId: String,
    score: Int,
    length: Int,
    foodEaten: Int,
    deathCause: Option[String]
) extends PlayerStats:
  def toJson: Obj = Obj(
    "playerId" -> Str(playerId),
    "score" -> Num(score),
    "length" -> Num(length),
    "foodEaten" -> Num(foodEaten),
    "deathCause" -> deathCause.map(Str(_)).getOrElse(Null)
  )

final case class MemoryPlayerStats(
    playerId: String,
    pairs: Int,
    attempts: Int,
    flips: Int,
    accuracy: Double
) extends PlayerStats:
  def toJson: Obj = Obj(
    "playerId" -> Str(playerId),
    "pairs" -> Num(pairs),
    "attempts" -> Num(attempts),
    "flips" -> Num(flips),
    "accuracy" -> Num(accuracy)
  )

sealed trait MatchStats:
  def fields: Seq[(String, Value)]

final case class RankingEntry(playerId: String, kills: Int, damage: Double):
  def toJson: Obj = Obj("playerId" -> Str(playerId), "kills" -> Num(kills), "damage" -> Num(damage))

final case class CombatMatchStats(
    durationMs: Option[Long],
    totalDamage: Double,
    ranking: List[RankingEntry],
    mvp: Option[String],
    winnerId: Option[String]
) extends MatchStats:
  def fields: Seq[(String, Value)] = Seq(
    "durationMs" -> durationMs.map(Num(_)).getOrElse(Null),
    "totalDamage" -> Num(totalDamage),
    "ranking" -> Arr.from(ranking.map(_.toJson)),
    "mvp" -> mvp.map(Str(_)).getOrElse(Null),
    "winnerId" -> winnerId.map(Str(_)).getOrElse(Null)
  )

final case class FastestLap(playerId: String, lapTimeMs: Long):
  def toJson: Obj = Obj("playerId" -> Str(playerId), "lapTimeMs" -> Num(lapTimeMs))

final case class RacingMatchStats(
    classification: Option[Value],
    fastestLap: Option[FastestLap],
    overtakes: Int
) extends MatchStats:
  def fields: Seq[(String, Value)] = Seq(
    "classification" -> classification.getOrElse(Null),
    "fastestLap" -> fastestLap.map(lap => lap.toJson: Value).getOrElse(Null),
    "overtakes" -> Num(overtakes)
  )

final case class ConnectFourMatchStats(
    durationMs: Option[Long],
    moves: Int,
    result: Option[String],
    winnerId: Option[String],
    aiDecisions: Int,
    totalNodes: Long,
    avgAiTimeMs: Option[Double]
) extends MatchStats:
  def fields: Seq[(String, Value)] = Seq(
    "durationMs" -> durationMs.map(Num(_)).getOrElse(Null),
    "moves" -> Num(moves),
    "result" -> result.map(Str(_)).getOrElse(Null),
    "winnerId" -> winnerId.map(Str(_)).getOrElse(Null),
    "aiDecisions" -> Num(aiDecisions),
    "totalNodes" -> Num(totalNodes),
    "avgAiTimeMs" -> avgAiTimeMs.map(Num(_)).getOrElse(Null)
  )

final case class SnakeMatchStats(
    durationMs: Option[Long],
    result: Option[String],
    score: Int,
    length: Int,
    foodEaten: Int,
    deathCause: Option[String]
) extends MatchStats:
  def fields: Seq[(String, Value)] = Seq(
    "durationMs" -> durationMs.map(Num(_)).getOrElse(Null),
    "result" -> result.map(Str(_)).getOrElse(Null),
    "score" -> Num(score),
    "length" -> Num(length),
    "foodEaten" -> Num(foodEaten),
    "deathCause" -> deathCause.map(Str(_)).getOrElse(Null)
  )

final case class MemoryMatchStats(
    durationMs: Option[Long],
    result: Option[String],
    pairs: Int,
    attempts: Int,
    flips: Int,
    accuracy: Double
) extends MatchStats:
  def fields: Seq[(String, Value)] = Seq(
    "durationMs" -> durationMs.map(Num(_)).getOrElse(Null),
    "result" -> result.map(Str(_)).getOrElse(Null),
    "pairs" -> Num(pairs),
    "attempts" -> Num(attempts),
    "flips" -> Num(flips),
    "accuracy" -> Num(accuracy)
  )

final case class MatchAnalysis(
    matchId: String,
    gameId: String,
    status: String,
    complete: Boolean,
    players: List[PlayerStats],
    stats: MatchStats
):
  def toJson: Obj = Obj.from(
    Seq(
      "matchId" -> Str(matchId),
      "gameId" -> Str(gameId),
      "status" -> Str(status),
      "complete" -> Bool(complete),
      "players" -> Arr.from(players.map(_.toJson))
    ) ++ stats.fields
  )

object JsonDecoding:
  def batch(value: Value): Batch =
    val obj = value.obj
    val matchId = obj("matchId").str
    val gameId = obj("gameId").str
    val status = obj("status").str
    val events = obj("events").arr.toList.map(event)
    Batch(matchId, gameId, status, events)

  private def event(value: Value): Event =
    val obj = value.obj
    def optionalString(key: String): Option[String] = obj.get(key).collect { case Str(s) => s }
    Event(
      eventId = obj("eventId").str,
      timestamp = obj("timestamp").str,
      gameId = obj("gameId").str,
      gameVersion = obj("gameVersion").str,
      matchId = obj("matchId").str,
      playerId = optionalString("playerId"),
      eventType = obj("type").str,
      action = optionalString("action"),
      data = obj.getOrElse("data", Obj())
    )