#!/usr/bin/env python3
import json
import sys


def seed(basket, number):
    return {"seed": {"basket": basket, "number": number}}


def from_match(code, place):
    return {"fromMatch": {"match": code, "place": place}}


def reseed(stage, rank):
    return {"reseed": {"stage": stage, "rank": rank}}


def match(code, venue, slots):
    return {
        "code": code,
        "title": f"Бой {code}",
        "venue": venue,
        "participantCount": len(slots),
        "slots": slots,
    }


def r16_run_stage(code, title, position, match_codes, first_seed_number):
    matches = []
    for index, match_code in enumerate(match_codes):
        seed_number = first_seed_number + index
        matches.append(
            match(
                match_code,
                index % 6 + 1,
                [
                    seed(1, seed_number),
                    seed(2, seed_number),
                    seed(3, seed_number),
                    seed(4, seed_number),
                ],
            )
        )
    return {
        "code": code,
        "title": title,
        "stage_type": "matches",
        "position": position,
        "matches": matches,
        "layout": {"columns": 1},
    }


def r8_stage():
    return {
        "code": "r8",
        "title": "1/8 финала",
        "stage_type": "matches",
        "position": 3,
        "layout": {"columns": 1},
        "matches": [
            match("M", 1, [from_match("A", 1), from_match("G", 1), from_match("B", 2), from_match("H", 2)]),
            match("N", 2, [from_match("B", 1), from_match("H", 1), from_match("A", 2), from_match("G", 2)]),
            match("O", 3, [from_match("C", 1), from_match("I", 1), from_match("D", 2), from_match("J", 2)]),
            match("P", 4, [from_match("D", 1), from_match("J", 1), from_match("C", 2), from_match("I", 2)]),
            match("Q", 5, [from_match("E", 1), from_match("K", 1), from_match("F", 2), from_match("L", 2)]),
            match("R", 6, [from_match("F", 1), from_match("L", 1), from_match("E", 2), from_match("K", 2)]),
        ],
    }


def reseed_after_r8_stage():
    teams = []
    for match_code in ["M", "N", "O", "P", "Q", "R"]:
        teams.append(from_match(match_code, 1))
        teams.append(from_match(match_code, 2))
    return {
        "code": "reseed_after_r8",
        "title": "Пересев перед 1/4",
        "stage_type": "reseed",
        "position": 4,
        "teams": teams,
        "sort": [
            {"metric": "place_sum", "dir": "asc"},
            {"metric": "total", "dir": "desc"},
            {"metric": "plus", "dir": "desc"},
            {"metric": "correct_50", "dir": "desc"},
            {"metric": "correct_40", "dir": "desc"},
            {"metric": "correct_30", "dir": "desc"},
            {"metric": "correct_20", "dir": "desc"},
            {"metric": "draw", "dir": "desc"},
        ],
    }


def r4_stage():
    stage = "reseed_after_r8"
    return {
        "code": "r4",
        "title": "1/4 финала",
        "stage_type": "matches",
        "position": 5,
        "layout": {"columns": 1},
        "matches": [
            match("S", 1, [reseed(stage, 1), reseed(stage, 8), reseed(stage, 9)]),
            match("T", 2, [reseed(stage, 4), reseed(stage, 5), reseed(stage, 12)]),
            match("U", 3, [reseed(stage, 2), reseed(stage, 7), reseed(stage, 10)]),
            match("V", 4, [reseed(stage, 3), reseed(stage, 6), reseed(stage, 11)]),
        ],
    }


def r2_stage():
    return {
        "code": "r2",
        "title": "1/2 финала",
        "stage_type": "matches",
        "position": 6,
        "layout": {"columns": 1},
        "matches": [
            match("W", 1, [from_match("S", 1), from_match("T", 2), from_match("U", 1), from_match("V", 2)]),
            match("X", 2, [from_match("S", 2), from_match("T", 1), from_match("U", 2), from_match("V", 1)]),
        ],
    }


def final_stage():
    return {
        "code": "final",
        "title": "Финал",
        "stage_type": "matches",
        "position": 7,
        "layout": {"columns": 1},
        "matches": [
            match("Y", 1, [from_match("W", 1), from_match("W", 2), from_match("X", 1), from_match("X", 2)]),
        ],
    }


def build_scheme():
    return {
        "schemaVersion": 2,
        "slug": "studchr-ek-2026",
        "title": "СтудЧР-2026, ЭК",
        "gameType": "ek",
        "questionValues": [10, 20, 30, 40, 50],
        "regularThemeCount": 12,
        "venues": [
            {"number": 1, "title": "Москва-1"},
            {"number": 2, "title": "Москва-2"},
            {"number": 3, "title": "Москва-3"},
            {"number": 4, "title": "Москва-4"},
            {"number": 5, "title": "Москва-5"},
            {"number": 6, "title": "Рим"},
        ],
        "stages": [
            r16_run_stage("r16_run1", "1/16 финала, заход 1", 1, ["A", "B", "C", "D", "E", "F"], 1),
            r16_run_stage("r16_run2", "1/16 финала, заход 2", 2, ["G", "H", "I", "J", "K", "L"], 7),
            r8_stage(),
            reseed_after_r8_stage(),
            r4_stage(),
            r2_stage(),
            final_stage(),
        ],
    }


json.dump(build_scheme(), sys.stdout, ensure_ascii=False, indent=2)
sys.stdout.write("\n")
