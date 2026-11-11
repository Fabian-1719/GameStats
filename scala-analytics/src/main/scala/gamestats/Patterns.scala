package gamestats

import java.time.{Duration, Instant}
import scala.util.Try
import ujson.*

final case class DetectedPattern(
    patternType: String,
    gameId: String,
    matchId: String,
    playerId: String,
    detail: Value
):
  def toJson: Obj = Obj(
    "type" -> Str(patternType),
    "gameId" -> Str(gameId),
    "matchId" -> Str(matchId),
    "playerId" -> Str(playerId),
    "detail" -> detail
  )

object PatternDetectors:
  import AnalyzerSupport.*

  private val killWindowMs = 10_000L
  private val reconnectWindowMs = 60_000L

  def detect(batch: Batch): List[DetectedPattern] = batch.gameId match
    case "combat-arena" => killStreaks(batch) ++ suspiciousAbandons(batch)
    case "racing-circuit" => comebacks(batch)
    case "connect-four" => lightningWins(batch)
    case "snake-arena" => foodStreaks(batch)
    case "memory-match" => perfectMemory(batch)
    case _ => Nil

  private val lightningMaxMoves = 10
  private val foodStreakCount = 5
  private val foodStreakWindowMs = 15_000L

  /** Victoria relámpago: el ganador de Cuatro en Línea necesitó 10 jugadas propias o menos. */
  def lightningWins(batch: Batch): List[DetectedPattern] =
    if batch.status != "COMPLETE" then Nil
    else
      val winner = batch.events.find(_.eventType == "MATCH_FINISHED").flatMap(event => string(event.data, "winnerId"))
      winner.toList.flatMap { winnerId =>
        val winnerMoves = batch.events.count(event => event.eventType == "MOVE" && event.playerId.contains(winnerId))
        Option.when(winnerMoves > 0 && winnerMoves <= lightningMaxMoves)(
          DetectedPattern("LIGHTNING_WIN", batch.gameId, batch.matchId, winnerId, Obj("moves" -> Num(winnerMoves)))
        )
      }

  /** Racha: 5 FOOD_EATEN del mismo jugador en menos de 15 s. Se reporta una vez, con la mejor ventana. */
  def foodStreaks(batch: Batch): List[DetectedPattern] =
    val food = batch.events.filter(_.eventType == "FOOD_EATEN")
    food.flatMap(_.playerId).distinct.sorted.flatMap { playerId =>
      val timed = food.filter(_.playerId.contains(playerId))
        .flatMap(event => instant(event).map(event -> _))
        .sortBy(_._2)
      // Para cada comida, cuántas comidas siguientes caben en menos de 15 s (ventana deslizante)
      val windows = timed.indices.map { start =>
        val inside = timed.drop(start).takeWhile { case (_, time) =>
          Duration.between(timed(start)._2, time).toMillis < foodStreakWindowMs
        }
        inside
      }
      windows.filter(_.size >= foodStreakCount).maxByOption(_.size).map { best =>
        DetectedPattern(
          "FOOD_STREAK",
          batch.gameId,
          batch.matchId,
          playerId,
          Obj(
            "count" -> Num(best.size),
            "start" -> Str(best.head._1.timestamp),
            "end" -> Str(best.last._1.timestamp)
          )
        )
      }
    }

  /** Memoria perfecta: la partida terminó con victoria y sin ningún PAIR_MISSED. */
  def perfectMemory(batch: Batch): List[DetectedPattern] =
    val finished = batch.events.find(_.eventType == "MATCH_FINISHED")
    val won = finished.exists(event => string(event.data, "result").contains("WIN"))
    val pairs = batch.events.count(_.eventType == "PAIR_MATCHED")
    val missed = batch.events.exists(_.eventType == "PAIR_MISSED")
    if batch.status != "COMPLETE" || !won || missed || pairs == 0 then Nil
    else
      val playerId = finished.flatMap(event => string(event.data, "winnerId"))
        .orElse(batch.events.find(_.eventType == "PAIR_MATCHED").flatMap(_.playerId))
      playerId.toList.map(id =>
        DetectedPattern("PERFECT_MEMORY", batch.gameId, batch.matchId, id, Obj("pairs" -> Num(pairs)))
      )

  def killStreaks(batch: Batch): List[DetectedPattern] =
    val eliminations = batch.events.filter(_.eventType == "PLAYER_ELIMINATED")
    eliminations.flatMap(_.playerId).distinct.sorted.flatMap { playerId =>
      val relevant = eliminations.filter(event =>
        event.playerId.contains(playerId) || string(event.data, "targetId").contains(playerId)
      ).map(event =>
        (event, string(event.data, "targetId").contains(playerId), event.playerId.contains(playerId))
      )
      val segments = relevant.foldLeft(List(List.empty[Event])) {
        case (currentSegments, (event, isDeath, isKill)) =>
          val afterDeath = if isDeath then List(Nil) ++ currentSegments else currentSegments
          if isKill then (event :: afterDeath.head) :: afterDeath.tail else afterDeath
      }
      segments.reverse.map(_.reverse).filter(_.nonEmpty).flatMap(killWindows(_, batch, playerId))
    }

  private def killWindows(
      kills: List[Event],
      batch: Batch,
      playerId: String
  ): List[DetectedPattern] =
    val timedKills = kills.flatMap(event => instant(event).map(time => event -> time))
    timedKills.indices.toList.flatMap { startIndex =>
      val startTime = timedKills(startIndex)._2
      val endIndex = (startIndex until timedKills.size).takeWhile { index =>
        val elapsed = Duration.between(startTime, timedKills(index)._2).toMillis
        elapsed >= 0 && elapsed <= killWindowMs
      }.lastOption
      endIndex
        .filter(_ - startIndex + 1 >= 3)
        .filter { end =>
          startIndex == 0 || Duration.between(timedKills(startIndex - 1)._2, timedKills(end)._2).toMillis > killWindowMs
        }
        .map { end =>
          val window = timedKills.slice(startIndex, end + 1).map(_._1)
          DetectedPattern(
            "KILL_STREAK",
            batch.gameId,
            batch.matchId,
            playerId,
            Obj(
              "count" -> Num(window.size),
              "start" -> Str(window.head.timestamp),
              "end" -> Str(window.last.timestamp)
            )
          )
        }
    }

  def suspiciousAbandons(batch: Batch): List[DetectedPattern] =
    batch.events.zipWithIndex.collect {
      case (disconnect, index)
          if disconnect.eventType == "PLAYER_DISCONNECTED" && disconnect.playerId.nonEmpty =>
        val playerId = disconnect.playerId.get
        val disconnectedAt = instant(disconnect)
        val reconnected = disconnectedAt.exists { start =>
          batch.events.drop(index + 1).exists { event =>
            event.eventType == "PLAYER_RECONNECTED" && event.playerId.contains(playerId) &&
              instant(event).exists { time =>
                val elapsed = Duration.between(start, time).toMillis
                elapsed >= 0 && elapsed <= reconnectWindowMs
              }
          }
        }
        val observedForSixtySeconds = for
          start <- disconnectedAt
          lastEvent <- batch.events.flatMap(instant).lastOption
        yield Duration.between(start, lastEvent).toMillis >= reconnectWindowMs
        val killsAtDisconnect = batch.events.take(index + 1)
          .filter(_.eventType == "PLAYER_ELIMINATED")
          .flatMap(_.playerId)
          .groupMapReduce(identity)(_ => 1)(_ + _)
        val playerKills = killsAtDisconnect.getOrElse(playerId, 0)
        val leaderKills = killsAtDisconnect.values.maxOption.getOrElse(0)
        if observedForSixtySeconds.contains(true) && !reconnected && playerKills < leaderKills then
          Some(DetectedPattern(
            "SUSPICIOUS_ABANDON",
            batch.gameId,
            batch.matchId,
            playerId,
            Obj(
              "kills" -> Num(playerKills),
              "leaderKills" -> Num(leaderKills),
              "disconnectedAt" -> Str(disconnect.timestamp)
            )
          ))
        else None
    }.flatten

  def comebacks(batch: Batch): List[DetectedPattern] =
    if batch.status != "COMPLETE" then Nil
    else
      val lapCount = batch.events.find(_.eventType == "MATCH_STARTED").flatMap(event => long(event.data, "laps"))
      val middleLap = lapCount.map(_ / 2).filter(_ > 0)
      val winner = firstPlace(batch)
      (middleLap, winner) match
        case (Some(lap), Some(winnerId)) =>
          val positions = batch.events
            .filter(event => event.eventType == "LAP_COMPLETED" && long(event.data, "lap").contains(lap))
            .flatMap(event =>
              for playerId <- event.playerId; position <- int(event.data, "position")
              yield playerId -> position
            )
            .groupBy(_._1)
            .map { case (playerId, results) => playerId -> results.last._2 }
          val fieldSize = positions.size
          positions.toList.collect {
            case (playerId, position) if playerId == winnerId && position > fieldSize / 2.0 =>
              DetectedPattern(
                "COMEBACK",
                batch.gameId,
                batch.matchId,
                playerId,
                Obj(
                  "lap" -> Num(lap),
                  "position" -> Num(position),
                  "fieldSize" -> Num(fieldSize)
                )
              )
          }
        case _ => Nil

  def firstPlace(batch: Batch): Option[String] =
    if batch.status != "COMPLETE" then None
    else
      batch.events.find(_.eventType == "MATCH_FINISHED")
        .flatMap(event => event.data.objOpt.flatMap(_.get("classification")))
        .flatMap {
          case Arr(entries) => entries.iterator.flatMap(classificationPlayer).take(1).toList.headOption
          case _ => None
        }

  private def classificationPlayer(value: Value): Option[String] = value match
    case Str(playerId) => Some(playerId)
    case obj: Obj => string(obj, "playerId").orElse(string(obj, "id"))
    case _ => None

  private def instant(event: Event): Option[Instant] = Try(Instant.parse(event.timestamp)).toOption