package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type scheme struct {
	SchemaVersion     int     `json:"schemaVersion"`
	Slug              string  `json:"slug"`
	Title             string  `json:"title"`
	QuestionValues    []int   `json:"questionValues"`
	RegularThemeCount int     `json:"regularThemeCount"`
	Venues            []venue `json:"venues"`
	Stages            []stage `json:"stages"`
}

type venue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
}

type stage struct {
	Code      string       `json:"code"`
	Title     string       `json:"title"`
	StageType string       `json:"stage_type"`
	Position  int          `json:"position"`
	Matches   []match      `json:"matches,omitempty"`
	Teams     []slot       `json:"teams,omitempty"`
	Sort      []sortRule   `json:"sort,omitempty"`
	Layout    *stageLayout `json:"layout,omitempty"`
}

type stageLayout struct {
	Columns int    `json:"columns"`
	Note    string `json:"note,omitempty"`
}

type match struct {
	Code             string `json:"code"`
	Title            string `json:"title"`
	Venue            int    `json:"venue"`
	ParticipantCount int    `json:"participantCount"`
	Slots            []slot `json:"slots"`
}

type slot struct {
	Seed        *seedRef      `json:"seed,omitempty"`
	FromMatch   *fromMatchRef `json:"fromMatch,omitempty"`
	Reseed      *reseedRef    `json:"reseed,omitempty"`
	Team        *teamRef      `json:"team,omitempty"`
	Placeholder string        `json:"placeholder,omitempty"`
	Label       string        `json:"label,omitempty"`
}

type seedRef struct {
	Basket   int `json:"basket"`
	Position int `json:"position"`
}

type fromMatchRef struct {
	Match string `json:"match"`
	Place int    `json:"place"`
}

type reseedRef struct {
	Stage string `json:"stage"`
	Rank  int    `json:"rank"`
}

type teamRef struct {
	ID      string   `json:"id,omitempty"`
	Name    string   `json:"name,omitempty"`
	Players []string `json:"players,omitempty"`
}

type sortRule struct {
	Metric string `json:"metric"`
	Dir    string `json:"dir"`
}

func main() {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(buildScheme()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func buildScheme() scheme {
	return scheme{
		SchemaVersion:     1,
		Slug:              "studchr-ek-2026",
		Title:             "СтудЧР-2026, ЭК",
		QuestionValues:    []int{10, 20, 30, 40, 50},
		RegularThemeCount: 12,
		Venues: []venue{
			{Number: 1, Title: "Москва-1"},
			{Number: 2, Title: "Москва-2"},
			{Number: 3, Title: "Москва-3"},
			{Number: 4, Title: "Москва-4"},
			{Number: 5, Title: "Москва-5"},
			{Number: 6, Title: "Рим"},
		},
		Stages: []stage{
			r16FirstRunStage(),
			r16SecondRunStage(),
			r8Stage(),
			r4Stage(),
			r2Stage(),
			finalStage(),
		},
	}
}

func r16FirstRunStage() stage {
	return r16RunStage("r16_run1", "1/16 финала, заход 1", 1, []string{"A", "B", "C", "D", "E", "F"}, 1)
}

func r16SecondRunStage() stage {
	return r16RunStage("r16_run2", "1/16 финала, заход 2", 2, []string{"G", "H", "I", "J", "K", "L"}, 7)
}

func r16RunStage(code, title string, position int, codes []string, firstSeedPosition int) stage {
	matches := make([]match, 0, len(codes))
	for index, code := range codes {
		seedPosition := firstSeedPosition + index
		matches = append(matches, match{
			Code:             code,
			Title:            "Бой " + code,
			Venue:            index%6 + 1,
			ParticipantCount: 4,
			Slots: []slot{
				team(1, seedPosition),
				team(2, seedPosition),
				team(3, seedPosition),
				team(4, seedPosition),
			},
		})
	}
	return stage{
		Code:      code,
		Title:     title,
		StageType: "matches",
		Position:  position,
		Matches:   matches,
		Layout:    &stageLayout{Columns: 1},
	}
}

func r8Stage() stage {
	return stage{
		Code:      "r8",
		Title:     "1/8 финала",
		StageType: "matches",
		Position:  3,
		Layout:    &stageLayout{Columns: 1},
		Matches: []match{
			nextMatch("M", 1, from("A", 1), from("G", 1), from("B", 2), from("H", 2)),
			nextMatch("N", 2, from("B", 1), from("H", 1), from("A", 2), from("G", 2)),
			nextMatch("O", 3, from("C", 1), from("I", 1), from("D", 2), from("J", 2)),
			nextMatch("P", 4, from("D", 1), from("J", 1), from("C", 2), from("I", 2)),
			nextMatch("Q", 5, from("E", 1), from("K", 1), from("F", 2), from("L", 2)),
			nextMatch("R", 6, from("F", 1), from("L", 1), from("E", 2), from("K", 2)),
		},
	}
}

func r4Stage() stage {
	reseedStage := "reseed_after_r8"
	return stage{
		Code:      "r4",
		Title:     "1/4 финала",
		StageType: "matches",
		Position:  4,
		Layout:    &stageLayout{Columns: 1},
		Matches: []match{
			nextMatch("S", 1, reseed(reseedStage, 1), reseed(reseedStage, 8), reseed(reseedStage, 9)),
			nextMatch("T", 2, reseed(reseedStage, 4), reseed(reseedStage, 5), reseed(reseedStage, 12)),
			nextMatch("U", 3, reseed(reseedStage, 2), reseed(reseedStage, 7), reseed(reseedStage, 10)),
			nextMatch("V", 4, reseed(reseedStage, 3), reseed(reseedStage, 6), reseed(reseedStage, 11)),
		},
	}
}

func r2Stage() stage {
	return stage{
		Code:      "r2",
		Title:     "1/2 финала",
		StageType: "matches",
		Position:  5,
		Layout:    &stageLayout{Columns: 1},
		Matches: []match{
			nextMatch("W", 1, from("S", 1), from("T", 2), from("U", 1), from("V", 2)),
			nextMatch("X", 2, from("S", 2), from("T", 1), from("U", 2), from("V", 1)),
		},
	}
}

func finalStage() stage {
	return stage{
		Code:      "final",
		Title:     "Финал",
		StageType: "matches",
		Position:  6,
		Layout:    &stageLayout{Columns: 1},
		Matches: []match{
			nextMatch("Y", 1, from("W", 1), from("W", 2), from("X", 1), from("X", 2)),
		},
	}
}

func nextMatch(code string, venue int, slots ...slot) match {
	return match{
		Code:             code,
		Title:            "Бой " + code,
		Venue:            venue,
		ParticipantCount: len(slots),
		Slots:            slots,
	}
}

func seed(basket, position int) slot {
	return slot{Seed: &seedRef{Basket: basket, Position: position}}
}

func team(basket, position int) slot {
	name := drawTeams[basket-1][position-1]
	return slot{Team: &teamRef{Name: name, Players: teamPlayers[name]}}
}

func from(code string, place int) slot {
	return slot{FromMatch: &fromMatchRef{Match: code, Place: place}}
}

func reseed(stage string, rank int) slot {
	return slot{Reseed: &reseedRef{Stage: stage, Rank: rank}}
}

var drawTeams = [][]string{
	{
		"ВШЭстером",
		"Bikes for Peace",
		"Стол",
		"Какой восторг!",
		"Детективы для элит",
		"Рыб'ending",
		"Дахусим",
		"Три буквы, да не те",
		"Progrevsql",
		"Полипоморы",
		"Где гласные(?)",
		"Дракончики",
	},
	{
		"Тина Терияки",
		"Ярослав Кудымов ведёт канал",
		"Privet piter",
		"Самурайская самоизоляция",
		"Квадратные штаны",
		"Постпопс",
		"Жалко Жаргала",
		"Нежность",
		"Монументальное одиночество Хаджи-Мурата",
		"Чарли Чапман и шоколадная фабрика",
		"Здесь могла быть ваша реклама",
		"Пангопуп",
	},
	{
		"Вина России",
		"жареные сушеные маленькие креветки",
		"шутки кончились",
		"Гид по куче снов (с)",
		"Передаём сау бул!",
		"Ворона шепчет Эврика!",
		"Кошка вид сзади",
		"Забыли Бланк Сдать",
		"Чикен Макларен",
		"Клыкастая грация",
		"Свет в конце тоннеля",
		"Клуб любителей конусов",
	},
	{
		"Злая щитоспинка",
		"6Д",
		"Дело в шляпе",
		"Норильские бобры",
		"Аве, Виктория!",
		"Галчата",
		"Слушай папу",
		"Ыаллыылар",
		"Энгимоно",
		"Сурок богоугодности",
		"Японские саморезы",
		"И Снова Торшер!",
	},
}

var teamPlayers = map[string][]string{
	"ВШЭстером":           {"Юлия Лапшина", "Савелий Кардашин", "Мария Крамкова", "Дамир Хамидуллин", "Андрей Акимов", "Максим Бобровицкий", "Захар Куренков"},
	"Bikes for Peace":     {"Леонид Карлинский", "Михаил Коблик", "Богдан Кулешов", "Максим Еремеев", "Алена Свепарская", "Филипп Тучак", "Денис Мишкин"},
	"Стол":                {"Сергей Лотин", "Варвара Капитульская", "Никита Косенков", "Андрей Славин", "Максим Титов", "Тимур Трубачеев"},
	"Какой восторг!":      {"Сергей Комов", "Александр Шлыков", "Дарья Кукшинова", "Марк Исаев", "Анатолий Сергунов", "Николай Овчинников", "Екатерина Чуркина"},
	"Детективы для элит":  {"Дмитрий Яшин", "Анастасия Банникова", "Даниил Чеченин", "Денис Потехин", "Амиль Садыков", "Игорь Ломовацкий"},
	"Рыб'ending":          {"Александр Василиженко", "Арина Баранова", "Анна Мошкорина", "Тимофей Маркин", "Виктория Корнеева", "Александр Осипов", "Санжи Сундуев", "Ахад Муратов"},
	"Дахусим":             {"Станислав Хамидулин", "Алексей Кокачев", "Матвей Кокушкин", "Михаил Подпиров", "Денис Птицын", "Алина Сизова", "Анна Собакина"},
	"Три буквы, да не те": {"Иван Катченко", "Илья Мазур", "Алексей Погорелов", "Есения Погорелова", "Майя Тепловодская", "Пётр Федосов", "Екатерина Целуковская"},
	"Progrevsql":          {"Николай Зотов", "Егор Королёв", "Фёдор Ососов", "Вероника Трубечкова", "Артём Трубников", "Арсений Прасковьин", "Иван Джантимиров"},
	"Полипоморы":          {"Даниил Лукин", "Александра Лелик", "Майя Алимова", "Мария Селягина", "Мария Герасимова", "Артем Шедько"},
	"Где гласные(?)":      {"Виктория Зубкова", "Ольга Ахсахалян", "Александра Хитрова", "Иван Копылов", "Яков Львовский", "Мария Кожевникова", "Олег Кочко"},
	"Дракончики":          {"Данила Гаврилов", "Тахир Хайруллин", "Дмитрий Греков", "Елизавета Матвеева", "Дмитрий Санковский", "Амир Насретдинов"},
	"Тина Терияки":        {"Анна Гордеева", "Егор Абрамов", "Олег Шукаев", "Алексей Сазонов", "Кирилл Тищенко", "Андрей Кислуха"},
	"Ярослав Кудымов ведёт канал": {"Владислав Орлов", "Ярослав Кудымов", "Максим Захаров", "Павел Рассадовский", "Елизавета Ци", "Иван Аникеевич", "Никита Жуков"},
	"Privet piter": {"Степан Савкин", "Семён Удальцов", "Мария Прокопьева", "Александр Богданов", "Павел Шилин", "Иван Космынин"},
	"Самурайская самоизоляция": {"Михаил Сухоруков", "Вероника Усова", "Мария Кукушкина", "Александр Марченко", "Елизавета Хазагаева", "Владимир Бадмаев", "Кирилл Тихонов"},
	"Квадратные штаны":         {"Павел Розов", "Никита Катити", "Наталья Фокина", "Екатерина Нефёдова", "Серафима Костенко", "Ксения Мошинская"},
	"Постпопс":                 {"Анатолий Алексин", "Нина Андреева", "Ангелина Бондаренко", "Федор Плешаков", "Полина Ройфман", "Олег Сериков", "Леонид Львовский", "Софья Орлова", "Дарья Черникова"},
	"Жалко Жаргала":            {"Алексей Свиридонов", "Василий Долотов", "Аркадий Демидов", "Полина Сныткина", "Кирилл Степанов", "Владимир Намолов", "Евгеньевич Василий"},
	"Нежность":                 {"Николай Афанасьев", "Юрий Крошкин", "Тимофей Никифоров", "Дмитрий Суханов", "Александр Ширшов"},
	"Монументальное одиночество Хаджи-Мурата": {"Михаил Федосеев", "Андрей Ярусов", "Иван Зотов", "Божена Балтина", "Алёна Пихтовникова", "Елизавета Карлинская", "Алёна Бариева"},
	"Чарли Чапман и шоколадная фабрика":       {"Дарья Карандашева", "Михаил Передеренко", "Юрий Бурдейный", "Дмитрий Экгауз", "Артём Синцов", "Арина Смирнова", "Роман Городков"},
	"Здесь могла быть ваша реклама":           {"Дмитрий Шаталов", "Любовь Семина", "Анастасия Парнас", "Ольга Бурлакова", "Андрей Поташев", "Антон Изотов", "Рахматулла Овезов", "Мария Аглетдинова", "Юлия Данильченко"},
	"Пангопуп":    {"Милена Александрой", "Евгения Демша", "Павел Заворотний", "Бажена Зинченко", "Артем Икунин", "Арсений Пименов", "Андрей Шинкаренко"},
	"Вина России": {"Илья Пикалов", "Павел Соколов", "Дмитрий Федоров", "Никита Мирошин", "Евгения Королева", "Елена Трифонова", "Ольга Антропова"},
	"жареные сушеные маленькие креветки": {"Егор Злодеев", "Анастасия Матвиенко", "Кирилл Мишкарёв", "Софья Григорович", "Дарья Ломцова", "Екатерина Токарева"},
	"шутки кончились":                    {"Екатерина Уткина", "Даниил Ермолаев", "Надин Абу Заалан", "Михаил Ельмекеев", "Дарья Горелова", "Елизавета Чугунова", "Яна Малышева", "Юлия Нецветаева"},
	"Гид по куче снов (с)":               {"Дмитрий Чесноков", "Иван Кононов", "Демид Харев", "Диана Кириленко", "Марк Костарев"},
	"Передаём сау бул!":                  {"Андрей Сенченко", "Алина Хаматнурова", "Мотя Ашихмина", "Виктор Вега", "Артемий Фарстов", "Алексей Поздеев", "Вячеслав Афанасьев"},
	"Ворона шепчет Эврика!":              {"Всеволод Папин", "Станислав Кащик", "Софья Камальдинова", "Татьяна Сосновских", "Герман Бжицких", "Леонид Рогальский"},
	"Кошка вид сзади":                    {"Елизавета Большунас", "Роман Губарьков", "Константин Еременко", "Михаил Ивакин", "Ирина Крупицкая", "Данила Кузнецов-Свинцов", "Яна Шаркова"},
	"Забыли Бланк Сдать":                 {"Егор Бутенко", "Виолетта Подхалюзина", "Лейя Стец", "Григорий Савров", "Ульяна Монаенкова"},
	"Чикен Макларен":                     {"Андрей Мясников", "Алексей Куров", "Дарья Колточенко", "Илья Николаев", "Фёдор Сусидко", "Артём Цаплин", "Семён Муравич"},
	"Клыкастая грация":                   {"Анна Булавчук", "Ирина Пеклуха", "Арина Цыганкова", "Ульяна Сусликова", "Егор Поляков", "Ирина Рублёва"},
	"Свет в конце тоннеля":               {"Алёна Дудкина", "Василий Иванов", "Николай Тугарев", "Марина Голецкая", "Дарья Дудкина", "Всеволод Дегай"},
	"Клуб любителей конусов":             {"Артём Дмитриев", "Олег Кочеров", "Анна Губарь", "Михаил Максимов", "Анна Исаева", "Дмитрий Борисов"},
	"Злая щитоспинка":                    {"Егор Дементьев", "Таисия Кирпикова", "Денис Красюк", "Михаил Московченко", "Амгалан Цыбенов", "Анна Рябикина"},
	"6Д":                                 {"Антон Ефименко", "Александр Волокушин", "Илья Дроздов", "Аким Облапенко", "Ева Рублева", "Константин Фессалийский", "Самат Абдуллин", "Иван Быков", "Салтанат Рахимжанова"},
	"Дело в шляпе":                       {"Кирилл Бобровицкий", "Игорь Соколов", "Михаил Казменко", "Максим Растворов", "Руслан Мухаметшин", "Елизавета Лойко"},
	"Норильские бобры":                   {"Михаил Сотничук", "Никита Шмырин", "Александра Дмитриева", "Рената Рандовцова", "Мария Василькова", "Сабина Шабдинова", "Софья Роговская"},
	"Аве, Виктория!":                     {"Владимир Воронцов", "Дмитрий Акулинин", "Никита Дикий", "Анастасия Киселева", "Иван Князев", "Арсений Мирошниченко", "Михаил Жабский"},
	"Галчата":                            {"Андрей Тьебо", "Ярослав Голыженков", "Сергей Горьков", "Семён Дудко", "Роман Евдокимов", "Диана Арутюнян"},
	"Слушай папу":                        {"Мария Перешивкина", "Иван Демидов", "Анастасия Максимова", "Всеволод Орлов", "Виктория Сошникова", "Василий Виноградов"},
	"Ыаллыылар":                          {"Богдан Онуфриенко", "Денис Селиванов", "Мария Маторная", "Фёдор Чернышёв", "Иван Гуськов", "Мария Рыбальченко"},
	"Энгимоно":                           {"Глеб Пислевич", "Егор Ковалев", "Владимир Ковалёв", "Алексей Сахаров", "Николай Фурман", "Кирилл Журавкин"},
	"Сурок богоугодности":                {"Елизавета Черная", "Никита Борисенков", "Анна Кашкина", "Рамиль Хакамов", "Илина Ларионова", "Диана Прокофьева", "Диана Астрелина"},
	"Японские саморезы":                  {"Лев Баталин", "Арина Демина", "Арина Жаркова", "Иван Зырянов", "Андрей Скрылёв", "Тимофей Попов"},
	"И Снова Торшер!":                    {"Даниил Кузьменко", "Никита Селезнев", "Алина Корочкина", "Яна Ерохина", "Никита Сидоров"},
}
