package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
)

type Response struct {
	Results []struct {
		ID                    int          `json:"id"`
		UsedIngredientCount   int          `json:"usedIngredientCount"`
		MissedIngredientCount int          `json:"missedIngredientCount"`
		MissedIngredients     []Ingredient `json:"missedIngredients"`
		UsedIngredients       []Ingredient `json:"usedIngredients"`
		UnusedIngredients     []Ingredient `json:"unusedIngredients"`
		Title                 string       `json:"title"`
		Nutrition             struct {
			Nutrients []struct {
				Name   string  `json:"name"`
				Amount float64 `json:"amount"`
				Unit   string  `json:"unit"`
			} `json:"nutrients"`
		} `json:"nutrition"`
	} `json:"results"`
}

type Ingredient struct {
	ID     int     `json:"id"`
	Amount float64 `json:"amount"`
	Unit   string  `json:"unit"`
	Name   string  `json:"name"`
}

type RecipeData struct {
	IDs                   [][]int
	Names                 [][]string
	UsedIngredientNames   [][]string
	MissedIngredientNames [][]string
	NutrientsNames        [][]string
	NutrientsAmounts      [][]float64
	NutrientsUnits        [][]string
}

func parseArguments() ([]string, int, error) {
	ingredients := flag.String("ingredients", "", "Comma-separated list of ingredients")
	numberOfRecipes := flag.Int("numberOfRecipes", 0, "Number of recipes to find")
	flag.Parse()

	if *ingredients == "" || *numberOfRecipes == 0 {
		return nil, 0, errors.New("usage: ./recipeFinder --ingredients=<ingredient1>,... --numberOfRecipes=<number>")
	}
	ingredientList := strings.Split(*ingredients, ",")

	return ingredientList, *numberOfRecipes, nil
}

func fetchURL(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("error fetching URL: %v", err)
	}

	defer func() {
		err := resp.Body.Close()
		if err != nil {
			log.Printf("error closing response body: %v", err)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %v", err)
	}

	return body, nil
}

func parseJSON(body []byte) (*Response, error) {
	if strings.Contains(string(body), "\"status\":\"failure\", \"code\":401,\"message\":\"You are not authorized") {
		return nil, errors.New("you are not authorized")
	}
	var response Response
	err := json.Unmarshal(body, &response)
	if err != nil {
		return nil, fmt.Errorf("error parsing JSON: %v", err)
	}
	return &response, nil
}

func parseResponse(response *Response) RecipeData {
	var allRecipes RecipeData

	for _, result := range response.Results {
		usedIngredientNames := _ingredientsToArray(result.UsedIngredients)
		missedIngredientNames := _ingredientsToArray(result.MissedIngredients)

		// Nutrients to arrays
		var nutrientsNames []string
		var nutrientsAmounts []float64
		var nutrientsUnits []string
		for _, nutrient := range result.Nutrition.Nutrients {
			if nutrient.Name == "Carbohydrates" || nutrient.Name == "Protein" || nutrient.Name == "Calories" {
				nutrientsNames = append(nutrientsNames, nutrient.Name)
				nutrientsAmounts = append(nutrientsAmounts, nutrient.Amount)
				nutrientsUnits = append(nutrientsUnits, nutrient.Unit)
			}
		}

		// Store ingredients and nutrients for this recipe
		allRecipes.Names = append(allRecipes.Names, []string{result.Title})
		allRecipes.IDs = append(allRecipes.IDs, []int{result.ID})
		allRecipes.UsedIngredientNames = append(allRecipes.UsedIngredientNames, usedIngredientNames)
		allRecipes.MissedIngredientNames = append(allRecipes.MissedIngredientNames, missedIngredientNames)
		allRecipes.NutrientsNames = append(allRecipes.NutrientsNames, nutrientsNames)
		allRecipes.NutrientsAmounts = append(allRecipes.NutrientsAmounts, nutrientsAmounts)
		allRecipes.NutrientsUnits = append(allRecipes.NutrientsUnits, nutrientsUnits)
	}
	return allRecipes
}

func _ingredientsToArray(ingredients []Ingredient) []string {
	var ingredientsNames []string
	for _, ingredient := range ingredients {
		ingredientsNames = append(ingredientsNames, ingredient.Name)
	}
	return ingredientsNames
}

func printRecipes(recipe RecipeData, numberOfRecipes int) {
	for index := range recipe.Names {
		if numberOfRecipes == 0 {
			return
		}
		numberOfRecipes--
		fmt.Printf("\n\nRecipe: %s\n", recipe.Names[index][0])
		fmt.Println("Used Ingredients:", strings.Join(recipe.UsedIngredientNames[index], ", "))
		fmt.Println("Missed Ingredients:", strings.Join(recipe.MissedIngredientNames[index], ", "))
		fmt.Println("Nutrients:")
		for i := range recipe.NutrientsNames[index] {
			fmt.Printf("%s: %.2f %s\n",
				recipe.NutrientsNames[index][i],
				recipe.NutrientsAmounts[index][i],
				recipe.NutrientsUnits[index][i])
		}
	}
}

func initDB() (*sql.DB, error) {
	cfg := mysql.Config{
		User:                 "root",
		Passwd:               "",
		Net:                  "tcp",
		Addr:                 "127.0.0.1:3306",
		DBName:               "recipe_finder",
		AllowNativePasswords: true,
	}

	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}

	pingErr := db.Ping()
	if pingErr != nil {
		return nil, pingErr
	}
	return db, nil
}

func checkIfQueryExistsInDB(db *sql.DB, queryIngredientList []string) (int, RecipeData, error) {
	var allRecipes RecipeData
	numberOfFoundRecipes := 0
	sort.Strings(queryIngredientList)
	sortedQuery := strings.Join(queryIngredientList, ",")

	rows, err := db.Query(
		"SELECT id, name, used_ingredients, missing_ingredients, calories, carbohydrates, protein "+
			"FROM recipes WHERE sorted_query = ?", sortedQuery)
	if err != nil {
		return 0, allRecipes, err
	}
	defer func(rows *sql.Rows) {
		err := rows.Close()
		if err != nil {
			log.Print("Error closing rows")
		}
	}(rows)

	for rows.Next() {
		var id int
		var name, usedIngredients, missingIngredients string
		var calories, carbohydrates, protein float64

		err := rows.Scan(&id, &name, &usedIngredients, &missingIngredients, &calories, &carbohydrates, &protein)
		if err != nil {
			return 0, allRecipes, err
		}

		allRecipes.IDs = append(allRecipes.IDs, []int{id})
		allRecipes.Names = append(allRecipes.Names, []string{name})
		allRecipes.UsedIngredientNames = append(allRecipes.UsedIngredientNames, strings.Split(usedIngredients, ", "))
		allRecipes.MissedIngredientNames = append(allRecipes.MissedIngredientNames, strings.Split(missingIngredients, ", "))
		allRecipes.NutrientsNames = append(allRecipes.NutrientsNames, []string{"Calories", "Carbohydrates", "Protein"})
		allRecipes.NutrientsAmounts = append(allRecipes.NutrientsAmounts, []float64{calories, carbohydrates, protein})
		allRecipes.NutrientsUnits = append(allRecipes.NutrientsUnits, []string{"kcal", "g", "g"})

		numberOfFoundRecipes++
	}

	if err = rows.Err(); err != nil {
		return 0, allRecipes, err
	}

	return numberOfFoundRecipes, allRecipes, nil
}

func addRecipesToDB(recipe RecipeData, db *sql.DB, queryIngredientList []string) error {
	sort.Strings(queryIngredientList)
	for index := range recipe.Names {
		_, err := db.Exec(
			"INSERT IGNORE INTO recipes"+
				"(id, sorted_query, name, used_ingredients, missing_ingredients, calories, carbohydrates, protein)"+
				"VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			recipe.IDs[index][0],
			strings.Join(queryIngredientList, ","),
			recipe.Names[index][0],
			strings.Join(recipe.UsedIngredientNames[index], ", "),
			strings.Join(recipe.MissedIngredientNames[index], ", "),
			recipe.NutrientsAmounts[index][0],
			recipe.NutrientsAmounts[index][1],
			recipe.NutrientsAmounts[index][2])
		if err != nil {
			return err
		}
	}
	return nil
}

func main() {
	apiKey := "675a32c1ba6e4aedb032fe32391c0aec"
	ingredientList, desiredNumberOfRecipes, err := parseArguments()
	if err != nil {
		fmt.Println(err)
		return
	}

	url := fmt.Sprintf("https://api.spoonacular.com/recipes/complexSearch?"+
		"apiKey=%s"+
		"&includeIngredients=%s"+
		"&number=%d"+
		"&fillIngredients=true"+
		"&sort=min-missing-ingredients"+
		"&addRecipeNutrition=true"+
		"&ignorePantry=true",
		apiKey, strings.Join(ingredientList, ","), desiredNumberOfRecipes)

	db, err := initDB()
	connectedToDB := err == nil

	numberOfRecipesFoundInDB, allRecipes, err := checkIfQueryExistsInDB(db, ingredientList)
	if err != nil {
		log.Print(err)
	}

	if numberOfRecipesFoundInDB >= desiredNumberOfRecipes {
		printRecipes(allRecipes, desiredNumberOfRecipes)
	} else {
		body, err := fetchURL(url)
		if err != nil {
			fmt.Println("Problem fetching recipes from API")
			log.Print(err)
			return
		}

		response, err := parseJSON(body)
		if err != nil {
			log.Print(err)
			return
		}

		allRecipes = parseResponse(response)

		if connectedToDB {
			//save recipe to database
			dbInsertingErr := addRecipesToDB(allRecipes, db, ingredientList)
			if dbInsertingErr != nil {
				log.Print(dbInsertingErr)
			}
		}
		printRecipes(allRecipes, desiredNumberOfRecipes)
	}
	defer func(db *sql.DB) {
		err := db.Close()
		if err != nil {
			log.Print("Error closing DB")
		}
	}(db)
}
