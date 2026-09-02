import { assertEquals } from "jsr:@std/assert";
import { playerName } from "./dist/fest-roster.js";

Deno.test("playerName is ФИО when the отчество is known", () => {
  assertEquals(
    playerName({ name: "Ковалёва Елена Александровна", firstName: "Елена", lastName: "Ковалёва", patronymic: "Александровна" }),
    "Ковалёва Елена Александровна",
  );
  assertEquals(playerName({ firstName: "Елена", lastName: "Ковалёва", patronymic: " Александровна " }), "Ковалёва Елена Александровна");
});

Deno.test("a player without one reads exactly as the server named them", () => {
  assertEquals(playerName({ name: "Пётр Новичок" }), "Пётр Новичок");
  assertEquals(playerName({ name: "Пётр Новичок", firstName: "Пётр", lastName: "Новичок", patronymic: "" }), "Пётр Новичок");
  assertEquals(playerName({}), "");
});
